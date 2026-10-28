package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// boxEngine runs containers: rootless Podman, or Docker as the fallback,
// whose daemon runs as root.
type boxEngine struct {
	name string // podman or docker
	bin  string
}

// These are replaced in tests.
var (
	boxCommand = func(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		command := exec.Command(name, args...)
		command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
		return command.Run()
	}
	boxLookPath = exec.LookPath
)

func detectBoxEngine() (boxEngine, string, error) {
	if path, err := boxLookPath("podman"); err == nil {
		return boxEngine{name: "podman", bin: path}, "", nil
	}
	if path, err := boxLookPath("docker"); err == nil {
		return boxEngine{name: "docker", bin: path},
			"Podman is not installed, so hi box uses Docker, whose daemon runs as root: an escape from the box would be root on this machine. Install Podman with hi install podman.", nil
	}
	return boxEngine{}, "", errors.New("hi box needs Podman; install it with hi install podman")
}

// boxRootGroups are this user's groups that make it root on the machine
// without a password: docker, lxd, and incus-admin start a container with
// the host's / in it. Boxes run as this user (--userns=keep-id), so an
// escape from one would be root. Tests replace it.
var boxRootGroups = func() []string {
	current, err := user.Current()
	if err != nil {
		return nil
	}
	ids, err := current.GroupIds()
	if err != nil {
		return nil
	}
	var groups []string
	for _, id := range ids {
		if group, err := user.LookupGroupId(id); err == nil && slices.Contains([]string{"docker", "lxd", "incus-admin"}, group.Name) {
			groups = append(groups, group.Name)
		}
	}
	return groups
}

// boxRootGroupsWarning says what boxRootGroups means, and how to leave
// them, or "".
func boxRootGroupsWarning() string {
	groups := boxRootGroups()
	if len(groups) == 0 {
		return ""
	}
	name := currentUserName()
	var leave []string
	for _, group := range groups {
		leave = append(leave, "sudo gpasswd -d "+name+" "+group)
	}
	return fmt.Sprintf("%s is in the %s %s, which makes it root on this machine without a password. "+
		"The boxes run as %s, so an escape from one would be root here. If nothing on this machine needs it, an admin runs:\n  %s\nIt takes effect after a reboot, or once %s has no sessions or services left running.",
		name, strings.Join(groups, " and "), map[bool]string{true: "groups", false: "group"}[len(groups) > 1], name, strings.Join(leave, "\n  "), name)
}

// output runs the engine and returns its standard output.
func (e boxEngine) output(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := boxCommand(nil, &stdout, &stderr, e.bin, args...)
	if err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %s", e.name, strings.Join(args[:min(2, len(args))], " "), qFirstLine(stderr.String(), err.Error()))
	}
	return stdout.String(), nil
}

// lastLines is a box's last n lines of output, or "" when there are
// none. The engine's logs replay what the box wrote to stderr on stderr,
// where an app's crash usually is, so both are kept, in their order.
func (e boxEngine) lastLines(container string, n int) string {
	var logs bytes.Buffer
	if boxCommand(nil, &logs, &logs, e.bin, "logs", "--tail", strconv.Itoa(n), container) != nil {
		return ""
	}
	return logs.String()
}

func (e boxEngine) interactive(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	return boxCommand(stdin, stdout, stderr, e.bin, args...)
}

func (e boxEngine) exists(kind, name string) bool {
	_, err := e.output(kind, "inspect", name)
	return err == nil
}

// state is a container's state, such as running or exited, or "" when it
// doesn't exist.
func (e boxEngine) state(name string) string {
	out, err := e.output("container", "inspect", "--format", "{{.State.Status}}", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (e boxEngine) defaultNetwork() string {
	if e.name == "docker" {
		return "bridge"
	}
	return "podman"
}

// userArgs run the box as the user, so files it writes are theirs.
func (e boxEngine) userArgs() []string {
	if e.name == "podman" {
		return []string{"--userns=keep-id"}
	}
	return []string{"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
}

// gpuArgs pass the AMD GPU in, with the render and video groups that open
// it. Podman needs crun for keep-groups.
func (e boxEngine) gpuArgs() []string {
	args := []string{"--device", "/dev/kfd", "--device", "/dev/dri"}
	if e.name == "podman" {
		return append(args, "--group-add", "keep-groups", "--security-opt", "label=disable")
	}
	for _, name := range []string{"render", "video"} {
		if group, err := user.LookupGroup(name); err == nil {
			args = append(args, "--group-add", group.Gid)
		}
	}
	return args
}

// boxHardening applies to every box: no capabilities, no privilege gain,
// and limits on processes and memory.
func boxHardening(memory string) []string {
	return []string{"--cap-drop=ALL", "--security-opt", "no-new-privileges", "--pids-limit", "4096", "--memory", memory}
}

// ---------------------------------------------------------------------------
// the base image

// boxContainerfile is hi's base image: Ubuntu with the tools agents use.
// The agents themselves come from the host, mounted read-only, so the box
// always runs the versions the user has. Node.js is the LTS from
// nodejs.org, checked against pinned checksums: Ubuntu 24.04's nodejs is
// 18, which current Vite and Vitest, and so the web template, can't run on.
// Bun, like uv, is the latest release when the image is built, checked
// against that release's SHASUMS256.txt. Ubuntu's packages are upgraded,
// so a build has the fixes Ubuntu has published (hi team rebuild).
const boxContainerfile = `FROM docker.io/library/ubuntu:24.04
RUN userdel -r ubuntu 2>/dev/null || true \
 && useradd --uid 1000 --home-dir /box/home --no-create-home --shell /bin/bash box \
 && apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl git less nano jq ripgrep fd-find tree unzip xz-utils zip \
    make build-essential pkg-config python3 python3-venv tmux procps file \
 && rm -rf /var/lib/apt/lists/* \
 && ln -s /usr/bin/fdfind /usr/local/bin/fd
RUN case "$(dpkg --print-architecture)" in \
      amd64) arch=x64 sum=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6 ;; \
      arm64) arch=arm64 sum=6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2 ;; \
      *) echo "no Node.js build for this architecture" >&2; exit 1 ;; \
    esac \
 && curl -fsSLo /tmp/node.tar.xz "https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-$arch.tar.xz" \
 && echo "$sum  /tmp/node.tar.xz" | sha256sum -c - \
 && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 --no-same-owner \
      --exclude=CHANGELOG.md --exclude=LICENSE --exclude=README.md \
 && rm /tmp/node.tar.xz \
 && node --version && npm --version
RUN case "$(dpkg --print-architecture)" in \
      amd64) arch=x64 ;; \
      arm64) arch=aarch64 ;; \
      *) echo "no Bun build for this architecture" >&2; exit 1 ;; \
    esac \
 && cd /tmp \
 && curl -fsSLO "https://github.com/oven-sh/bun/releases/latest/download/bun-linux-$arch.zip" \
 && curl -fsSL https://github.com/oven-sh/bun/releases/latest/download/SHASUMS256.txt \
    | grep " bun-linux-$arch.zip\$" | sha256sum -c - \
 && unzip -jq "bun-linux-$arch.zip" "bun-linux-$arch/bun" -d /usr/local/bin \
 && ln -s bun /usr/local/bin/bunx \
 && rm "bun-linux-$arch.zip" \
 && bun --version
RUN curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin UV_NO_MODIFY_PATH=1 sh
ENV HOME=/box/home LANG=C.UTF-8 TERM=xterm-256color
WORKDIR /box
`

// dockerfileSources lists the files a Dockerfile's COPY and ADD lines take
// from the build context, in order; folders are walked. COPY --from takes
// from another image, so it adds nothing.
func dockerfileSources(dockerfile []byte, context string) []string {
	var paths []string
	for _, line := range strings.Split(strings.ReplaceAll(string(dockerfile), "\\\n", " "), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || (!strings.EqualFold(fields[0], "COPY") && !strings.EqualFold(fields[0], "ADD")) {
			continue
		}
		var sources []string
		for _, field := range fields[1 : len(fields)-1] {
			if strings.HasPrefix(field, "--from") {
				sources = nil
				break
			}
			if !strings.HasPrefix(field, "--") {
				sources = append(sources, field)
			}
		}
		for _, source := range sources {
			if strings.Contains(source, "://") {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(context, filepath.FromSlash(source)))
			for _, match := range matches {
				filepath.WalkDir(match, func(path string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						paths = append(paths, path)
					}
					return nil
				})
			}
		}
	}
	return paths
}

func boxBaseImage() string {
	digest := sha256.Sum256([]byte(boxContainerfile))
	return "localhost/hi-box:" + hex.EncodeToString(digest[:])[:12]
}

// ensureImage builds hi's base image the first time, or a project's own
// Dockerfile, and returns the image to run.
func (e boxEngine) ensureBaseImage(stdout, stderr io.Writer) (string, error) {
	image := boxBaseImage()
	if e.exists("image", image) {
		return image, nil
	}
	fmt.Fprintf(stdout, "Building the hi box image %s (once, a few minutes)...\n", image)
	return image, e.buildBaseImage(stdout, stderr)
}

// rebuildBaseImage builds hi's base image again from a fresh Ubuntu and
// no cached layers, so it has Ubuntu's current packages. The tag moves to
// the new image; boxes running the old one keep it until they restart.
func (e boxEngine) rebuildBaseImage(stdout, stderr io.Writer) (string, error) {
	image := boxBaseImage()
	fmt.Fprintf(stdout, "Building the hi box image %s again, with Ubuntu's current packages (a few minutes)...\n", image)
	return image, e.buildBaseImage(stdout, stderr, "--pull", "--no-cache")
}

func (e boxEngine) buildBaseImage(stdout, stderr io.Writer, flags ...string) error {
	image := boxBaseImage()
	dir, err := os.MkdirTemp("", "hi-box-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte(boxContainerfile), 0o644); err != nil {
		return err
	}
	args := append(append([]string{"build"}, flags...), "-t", image, "-f", filepath.Join(dir, "Containerfile"), dir)
	if err := e.interactive(nil, stdout, stderr, args...); err != nil {
		return fmt.Errorf("building the box image failed: %w", err)
	}
	return nil
}

// ensureProjectImage builds a project's Dockerfile from devcontainer.json,
// tagged by the Dockerfile's content, its build args, and the files it
// copies in, so a new uv.lock gives a new image.
func (e boxEngine) ensureProjectImage(dockerfile, context string, args map[string]string, stdout, stderr io.Writer) (string, error) {
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write(data)
	digest.Write([]byte("\x00" + context))
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	build := []string{"build", "-t", "", "-f", dockerfile}
	for _, key := range keys {
		digest.Write([]byte("\x00" + key + "=" + args[key]))
		build = append(build, "--build-arg", key+"="+args[key])
	}
	for _, path := range dockerfileSources(data, context) {
		digest.Write([]byte("\x00" + path + "\x00"))
		if content, err := os.ReadFile(path); err == nil {
			digest.Write(content)
		}
	}
	image := "localhost/hi-box-project:" + hex.EncodeToString(digest.Sum(nil))[:12]
	if e.exists("image", image) {
		return image, nil
	}
	build[2] = image
	fmt.Fprintf(stdout, "Building the project's image from %s...\n", dockerfile)
	if err := e.interactive(nil, stdout, stderr, append(build, context)...); err != nil {
		return "", fmt.Errorf("building %s failed: %w", dockerfile, err)
	}
	return image, nil
}
