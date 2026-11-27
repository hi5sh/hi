package cli

// A sealed copy of an app's snapshot key beside its offsite repository
// (hi_server_vault.md, Snapshots, backups, and moves): hi-key.sealed, the
// key sealed (nacl/box, anonymous) to the public half derived from the
// recovery key, which the vault keeps. So the recovery key alone, with
// the bucket's keys, reads the snapshots after the machine is lost:
// hi app restore <name> --from s3:https://<host>/<bucket>[/<path>].

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"golang.org/x/crypto/nacl/box"
)

const appSealedKeyName = "hi-key.sealed"

// appSealedKey is hi-key.sealed: what opens it is the recovery key's
// words, through Salt; Public says which recovery key it was sealed to.
type appSealedKey struct {
	Kind    string    `json:"kind"` // "hi snapshot key"
	App     string    `json:"app"`
	Sealed  []byte    `json:"sealed"`
	Salt    []byte    `json:"salt"`
	Public  []byte    `json:"public"`
	Created time.Time `json:"created"`
}

// appOffsiteObject is where a file beside the repository is: its URL in
// the bucket, or its path where the repository is a folder.
func appOffsiteObject(offsite, name string) (url, path string, err error) {
	if strings.HasPrefix(offsite, "/") {
		return "", filepath.Join(offsite, name), nil
	}
	rest, ok := strings.CutPrefix(offsite, "s3:")
	if !ok {
		return "", "", fmt.Errorf("%s isn't an S3 bucket as restic names it (s3:https://<host>/<bucket>)", offsite)
	}
	if !strings.HasPrefix(rest, "https://") && !strings.HasPrefix(rest, "http://") {
		rest = "https://" + rest
	}
	return strings.TrimRight(rest, "/") + "/" + name, "", nil
}

// appBucket is the bucket's keys and region.
type appBucket struct {
	KeyID, Secret, Region string
}

// appBucketCall sends one request to the bucket, signed (SigV4).
var appBucketCall = func(method, url string, bucket appBucket, body []byte) ([]byte, int, error) {
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	request.Header.Set("X-Amz-Content-Sha256", hash)
	credentials := aws.Credentials{AccessKeyID: bucket.KeyID, SecretAccessKey: bucket.Secret}
	if err := v4.NewSigner().SignHTTP(context.Background(), credentials, request, hash, "s3", firstNonEmpty(bucket.Region, appDefaultRegion), time.Now()); err != nil {
		return nil, 0, err
	}
	response, err := (&http.Client{Timeout: time.Minute}).Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return data, response.StatusCode, nil
}

func appBucketPut(offsite string, bucket appBucket, data []byte) error {
	return appBucketPutObject(offsite, appSealedKeyName, bucket, data)
}

func appBucketGet(offsite string, bucket appBucket) ([]byte, error) {
	data, found, err := appBucketGetObject(offsite, appSealedKeyName, bucket)
	if err == nil && !found {
		err = fmt.Errorf("the bucket has no %s beside the snapshots: it was made by hi before the vault, or the bucket's path is another", appSealedKeyName)
	}
	return data, err
}

// appBucketPutObject writes one file under offsite, the bucket's path.
func appBucketPutObject(offsite, name string, bucket appBucket, data []byte) error {
	url, path, err := appOffsiteObject(offsite, name)
	if err != nil {
		return err
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return writeFileAtomic(path, data, 0o600)
	}
	answer, status, err := appBucketCall("PUT", url, bucket, data)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("the bucket answered %d: %s", status, qFirstLine(strings.TrimSpace(string(answer)), ""))
	}
	return nil
}

// appBucketGetObject reads one file under offsite; found is false when
// the bucket answers that it isn't there.
func appBucketGetObject(offsite, name string, bucket appBucket) (data []byte, found bool, err error) {
	url, path, err := appOffsiteObject(offsite, name)
	if err != nil {
		return nil, false, err
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
	data, status, err := appBucketCall("GET", url, bucket, nil)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound && !strings.Contains(string(data), "NoSuchBucket") {
		return nil, false, nil
	}
	if status/100 != 2 {
		return nil, false, fmt.Errorf("the bucket answered %d: %s", status, qFirstLine(strings.TrimSpace(string(data)), ""))
	}
	return data, true, nil
}

// appSealOffsiteKey puts hi-key.sealed beside the app's offsite
// repository, when it isn't there for this key, recovery key, and bucket.
func appSealOffsiteKey(team string) error {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	backups := config.backups()
	if backups.Offsite == "" || !vaultExists() {
		return nil
	}
	keys, err := loadVaultKeys()
	if err != nil {
		return err
	}
	if len(keys.RecoveryPublic) != 32 {
		return nil
	}
	key, err := readSecretFile(appSnapshotKeySecret(team))
	if err != nil {
		return err
	}
	mark := sha256.Sum256(slicesConcat(keys.RecoveryPublic, []byte(backups.Offsite), key))
	marker := hex.EncodeToString(mark[:])
	if loadTeamSnapshotStatus(team).KeySealed == marker {
		return nil
	}
	sealed, err := appSealKey(team, key, keys)
	if err != nil {
		return err
	}
	bucket, err := appOffsiteBucket(team, backups)
	if err != nil {
		return err
	}
	err = appBucketPut(backups.Offsite, bucket, sealed)
	updateTeamSnapshotStatus(team, func(s *appSnapshotStatus) {
		if err != nil {
			s.KeySealFailed = err.Error()
			return
		}
		s.KeySealed, s.KeySealFailed = marker, ""
	})
	if err != nil {
		return fmt.Errorf("%s beside the snapshots: %w", appSealedKeyName, err)
	}
	return nil
}

func slicesConcat(parts ...[]byte) []byte {
	var all []byte
	for _, part := range parts {
		all = append(all, part...)
		all = append(all, 0)
	}
	return all
}

// appSealKey seals key to the recovery key's public half.
func appSealKey(team string, key []byte, keys vaultKeys) ([]byte, error) {
	var public [32]byte
	copy(public[:], keys.RecoveryPublic)
	sealed, err := box.SealAnonymous(nil, key, &public, rand.Reader)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(appSealedKey{Kind: "hi snapshot key", App: team, Sealed: sealed, Salt: keys.RecoveryBoxSalt, Public: keys.RecoveryPublic, Created: time.Now().UTC()}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// appOpenSealedKey opens hi-key.sealed with the recovery key's words.
func appOpenSealedKey(data []byte, words string) ([]byte, string, error) {
	var sealed appSealedKey
	if err := json.Unmarshal(data, &sealed); err != nil || sealed.Kind != "hi snapshot key" {
		return nil, "", fmt.Errorf("%s isn't hi's sealed snapshot key", appSealedKeyName)
	}
	entropy, err := vaultRecoveryDecode(words)
	if err != nil {
		return nil, "", err
	}
	private, public, err := vaultRecoveryBox(entropy, sealed.Salt)
	if err != nil {
		return nil, "", err
	}
	if !bytes.Equal(public, sealed.Public) {
		return nil, "", errors.New("that recovery key isn't the one the snapshot key was sealed to: a newer or older one, or another machine's")
	}
	var pub, priv [32]byte
	copy(pub[:], public)
	copy(priv[:], private)
	key, ok := box.OpenAnonymous(nil, sealed.Sealed, &pub, &priv)
	if !ok {
		return nil, "", errors.New("the sealed key doesn't open: it was changed")
	}
	return key, sealed.App, nil
}

// vaultResealOffsite seals every app's snapshot key again, after a new
// recovery key; one that fails is tried again at its next copy.
func vaultResealOffsite(log io.Writer) {
	for _, name := range machineAppNames() {
		if err := appSealOffsiteKey(name); err != nil {
			fmt.Fprintf(log, "hi: app %s: %v; tried again at its next copy offsite\n", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// hi app restore <name> --from <bucket>

// appRestoreFrom brings an app back on this machine from its offsite
// copy alone: the bucket's keys and the recovery key open hi-key.sealed,
// the snapshots come into a local repository, and the chosen one into a
// new folder, which hi then adopts.
func appRestoreFrom(name, offsite, region, folder, which string, stdin io.Reader, stdout io.Writer) error {
	if _, err := appRestoreFromCheck(name, folder); err != nil {
		return err
	}
	bucket := appBucket{Region: region}
	var err error
	if strings.HasPrefix(offsite, "s3:") {
		if bucket.KeyID, err = readSecretLine(stdin, stdout, "The bucket's access key ID"); err != nil {
			return err
		}
		if bucket.Secret, err = readSecretLine(stdin, stdout, "Its secret access key"); err != nil {
			return err
		}
	}
	words, err := readSecretLine(stdin, stdout, "The recovery key's 24 words, of the machine the app was on")
	if err != nil {
		return err
	}
	return appRestoreFromWith(name, offsite, bucket, words, folder, which, false, stdout)
}

// appRestoreFromCheck says whether an app can come back here from a
// bucket, and the folder it comes back into.
func appRestoreFromCheck(name, folder string) (string, error) {
	if !appNamePattern.MatchString(name) {
		return "", fmt.Errorf("%q isn't an app's name", name)
	}
	if _, err := loadTeam(name); err == nil {
		return "", fmt.Errorf("app %s is on this machine: hi app restore %s <snapshot> restores it from its own snapshots", name, name)
	}
	if err := appNeedsServer("hi app restore --from"); err != nil {
		return "", err
	}
	if !appResticInstalled() {
		return "", errors.New("restic isn't installed: hi install restic adds it")
	}
	dir := appSnapshotsDir(name)
	if fileExists(filepath.Join(dir, "repo", "config")) {
		return "", fmt.Errorf("%s holds snapshots of a removed app called %s: hi app up with its kept folder brings it back, or hi app rm --purge %s deletes them", dir, name, name)
	}
	target, err := filepath.Abs(firstNonEmpty(folder, "app-"+name))
	if err != nil {
		return "", err
	}
	if fileExists(target) {
		return "", fmt.Errorf("%s is there already; --folder names another", target)
	}
	return target, nil
}

// appRestoreFromWith brings an app back from a bucket with its keys and
// the recovery key's words in hand. fromServer: hi server restore brought
// the old vault back, so the app's settings and tokens are there, and the
// app follows the server's bucket.
func appRestoreFromWith(name, offsite string, bucket appBucket, words, folder, which string, fromServer bool, stdout io.Writer) error {
	target, err := appRestoreFromCheck(name, folder)
	if err != nil {
		return err
	}
	dir := appSnapshotsDir(name)
	region := bucket.Region
	data, err := appBucketGet(offsite, bucket)
	if err != nil {
		return err
	}
	key, sealedFor, err := appOpenSealedKey(data, words)
	if err != nil {
		return err
	}
	if sealedFor != name {
		fmt.Fprintf(stdout, "The bucket's key was sealed for app %s; it comes back as %s.\n", sealedFor, name)
	}
	if !vaultExists() {
		if err := ensureVault(stdout); err != nil {
			return err
		}
	}
	if err := writeSecretFile(appSnapshotKeySecret(name), key, 0o600); err != nil {
		return err
	}
	undo := func() {
		vaultPurgeApp(name)
		os.RemoveAll(dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	local := filepath.Join(dir, "repo")
	env := slicesConcatStrings([]string{"RESTIC_REPOSITORY=" + local}, appResticKeyEnv(name, "RESTIC_PASSWORD"),
		[]string{"RESTIC_FROM_REPOSITORY=" + offsite}, appResticKeyEnv(name, "RESTIC_FROM_PASSWORD"),
		[]string{"AWS_ACCESS_KEY_ID=" + bucket.KeyID, "AWS_SECRET_ACCESS_KEY=" + bucket.Secret, "AWS_DEFAULT_REGION=" + firstNonEmpty(region, appDefaultRegion)})
	fmt.Fprintf(stdout, "Copying the snapshots from %s…\n", offsite)
	if _, err := appRestic(env, dir, "init", "--quiet", "--copy-chunker-params"); err != nil {
		undo()
		return fmt.Errorf("making a repository here: %w", err)
	}
	if _, err := appRestic(env, dir, "copy", "--quiet"); err != nil {
		undo()
		return fmt.Errorf("copying the snapshots: %w", err)
	}
	snapshot, err := appPickRestored(appResticEnv(name), dir, which)
	if err != nil {
		undo()
		return err
	}
	fmt.Fprintf(stdout, "Restoring snapshot %s, taken %s, into %s…\n", snapshot.ID, snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), target)
	if _, err := appRestic(appResticEnv(name), dir, "restore", snapshot.ID, "--quiet", "--target", target); err != nil {
		os.RemoveAll(target)
		undo()
		return fmt.Errorf("restoring: %w", err)
	}
	adopted, err := appAdopt(target, stdout)
	if err != nil {
		return fmt.Errorf("the snapshot is in %s, but hi didn't take it as an app: %w", target, err)
	}
	if adopted != name {
		return fmt.Errorf("the snapshot is app %s's; it is in %s as %s", name, target, adopted)
	}
	// Its copies offsite go on, to the same bucket (or the server's).
	if strings.HasPrefix(offsite, "s3:") && !fromServer {
		if err := updateTeamSecretsFile(name, func(s *appSecrets) error {
			s.BackupKeyID, s.BackupSecret = bucket.KeyID, bucket.Secret
			return nil
		}, false); err != nil {
			fmt.Fprintf(stdout, "hi: the bucket's keys aren't kept: %v; hi app backups %s --offsite %s sets them\n", err, name, offsite)
		}
	}
	vaultAudit(vaultActor(), "vault.restore-from", "hi/app/"+name, "the snapshot key from "+appSealedKeyName)
	if fromServer {
		fmt.Fprintf(stdout, "App %s is back in %s, as it was %s, with its settings and tokens from the server's vault.\n", name, target, snapshot.Time.Local().Format("Mon Jan 2 at 15:04"))
		return nil
	}
	fmt.Fprintf(stdout, "App %s is back in %s, as it was %s. Its settings and tokens were in the old machine's vault: set them again (hi app env %s, and its Slack app on the admin page), then hi app up %s.\n",
		name, target, snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), name, name)
	return nil
}

func slicesConcatStrings(parts ...[]string) []string {
	var all []string
	for _, part := range parts {
		all = append(all, part...)
	}
	return all
}

// appPickRestored is a snapshot in the repository: the latest, or by ID.
func appPickRestored(env []string, dir, which string) (appSnapshot, error) {
	out, err := appRestic(env, dir, "snapshots", "--json")
	if err != nil {
		return appSnapshot{}, err
	}
	var listed []struct {
		ID    string    `json:"short_id"`
		Time  time.Time `json:"time"`
		Paths []string  `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return appSnapshot{}, fmt.Errorf("restic's list of snapshots: %w", err)
	}
	if len(listed) == 0 {
		return appSnapshot{}, errors.New("the bucket has no snapshots")
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Time.Before(listed[j].Time) })
	for i := len(listed) - 1; i >= 0; i-- {
		if which == "" || which == "latest" || strings.HasPrefix(listed[i].ID, which) {
			return appSnapshot{ID: listed[i].ID, Time: listed[i].Time}, nil
		}
	}
	return appSnapshot{}, fmt.Errorf("no snapshot %s in the bucket", which)
}
