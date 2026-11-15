package cli

// An app's paths in the vault (hi/app/<app>/...) as the app is renamed,
// moved, removed, and purged.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func appVaultPrefix(name string) string { return "hi/app/" + name }

// movePrefix moves every path under old to the same place under new,
// each version encrypted again, since a version is bound to its path.
func (v *vaultStore) movePrefixLocal(old, new string) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	files, err := v.pathsLocal(old)
	if err != nil {
		return err
	}
	if there, err := v.pathsLocal(new); err != nil {
		return err
	} else if len(there) > 0 {
		return fmt.Errorf("the vault has paths under %s already", new)
	}
	for _, file := range files {
		moved := vaultPathFile{Path: new + strings.TrimPrefix(file.Path, old), Removed: file.Removed}
		for _, version := range file.Versions {
			if version.Forgotten == nil {
				data, err := v.decryptLocal(file.Path, version)
				if err != nil {
					return err
				}
				plain, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if version.Sealed, err = v.sealVersion(moved.Path, version.Version, plain); err != nil {
					return err
				}
			}
			moved.Versions = append(moved.Versions, version)
		}
		if err := v.save(moved); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := os.Remove(vaultPathFileName(v.dir, file.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// vaultRenameApp moves an app's paths to its new name.
func vaultRenameApp(old, name string) error {
	if !vaultExists() {
		return nil
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	if err := store.movePrefix(appVaultPrefix(old), appVaultPrefix(name)); err != nil {
		return fmt.Errorf("moving the app's settings in the vault: %w", err)
	}
	vaultAudit(vaultActor(), "vault.rename", appVaultPrefix(old), "to "+appVaultPrefix(name))
	return nil
}

// vaultRemoveApp keeps a removed app's paths, read by nothing; it says
// whether there were any.
func vaultRemoveApp(name string) (bool, error) {
	if !vaultExists() {
		return false, nil
	}
	store, err := openVault()
	if err != nil {
		return false, err
	}
	files, err := store.paths(appVaultPrefix(name))
	if err != nil || len(files) == 0 {
		return false, err
	}
	if _, err := store.markRemoved(appVaultPrefix(name)); err != nil {
		return false, err
	}
	vaultAudit(vaultActor(), "vault.app-removed", appVaultPrefix(name), "kept")
	return true, nil
}

// vaultRemovedApp is whether a removed app's paths are kept under name.
func vaultRemovedApp(name string) bool {
	if !vaultExists() {
		return false
	}
	store, err := openVault()
	if err != nil {
		return false
	}
	files, _ := store.paths(appVaultPrefix(name))
	for _, file := range files {
		if file.Removed != nil {
			return true
		}
	}
	return false
}

// vaultReviveApp gives a removed app's kept paths back to the app.
func vaultReviveApp(name string) error {
	store, err := openVault()
	if err != nil {
		return err
	}
	if _, err := store.revive(appVaultPrefix(name)); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.app-back", appVaultPrefix(name), "its kept settings")
	return nil
}

// vaultPurgeApp deletes an app's paths, versions and all.
func vaultPurgeApp(name string) (int, error) {
	if !vaultExists() {
		return 0, nil
	}
	store, err := openVault()
	if err != nil {
		return 0, err
	}
	n, err := store.purge(appVaultPrefix(name))
	vaultSharedPurgeApp(name)
	if err == nil && n > 0 {
		vaultAudit(vaultActor(), "vault.purge", appVaultPrefix(name), fmt.Sprintf("%d paths", n))
	}
	return n, err
}

// vaultExportApp writes an app's secrets as the files they were, for hi
// app move to carry over its own connection; the machine there takes
// them into its vault as it starts the app (vaultImportApp). done
// removes the files here again.
func vaultExportApp(name string) (done func(), err error) {
	files := []secretFile{appSecretsSecret(name), appSnapshotKeySecret(name)}
	var written []secretFile
	done = func() {
		if vaultKeepFiles() {
			return // an older hi still reads them there
		}
		for _, f := range written {
			// Back to what the vault keeps on disk: an older hi's heartbeat URL, if any.
			if data, err := readSecretFile(f); err == nil {
				if _, disk, err := splitSecret(f, data); err == nil && disk != nil {
					writeFileAtomic(f.disk, disk, 0o600)
					continue
				}
			}
			os.Remove(f.disk)
		}
	}
	for _, f := range files {
		if !f.inVault() {
			continue
		}
		if disk, err := os.ReadFile(f.disk); err == nil && diskHoldsSecret(f, disk) {
			continue // still the file it was: it goes as it is, and stays
		}
		data, err := readSecretFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			done()
			return nil, err
		}
		if err := writeFileAtomic(f.disk, data, 0o600); err != nil {
			done()
			return nil, err
		}
		written = append(written, f)
	}
	return done, nil
}

// vaultImportApp takes a received app's secret files into this machine's
// vault, where it has one.
func vaultImportApp(name string) error {
	for _, f := range []secretFile{appSecretsSecret(name), appSnapshotKeySecret(name)} {
		if _, err := moveSecretFile(f); err != nil {
			return err
		}
	}
	return nil
}
