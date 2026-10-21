/*
Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package libvirt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/crypto/ssh"
)

// Local libvirt connection URIs used by virsh on the remote host.
const (
	// ConnURISystem is the privileged system daemon connection.
	ConnURISystem = "qemu:///system"
	// ConnURISession is the per-user unprivileged daemon connection.
	ConnURISession = "qemu:///session"
)

// LocalConnectionURI maps a LibvirtHost URI (e.g. qemu+ssh://user@host/session)
// to the local connection URI virsh should use once SSH'd into the host.
// An empty or "/system" path selects the system daemon; "/session" selects
// the per-user daemon. Any other path is an error.
func LocalConnectionURI(hostURI string) (string, error) {
	parsed, err := url.Parse(hostURI)
	if err != nil {
		return "", fmt.Errorf("invalid libvirt URI %q: %w", hostURI, err)
	}
	switch parsed.Path {
	case "", "/", "/system":
		return ConnURISystem, nil
	case "/session":
		return ConnURISession, nil
	default:
		return "", fmt.Errorf("unsupported libvirt connection path %q in URI %q (expected /system or /session)", parsed.Path, hostURI)
	}
}

// VirshClient implements Client using virsh commands over SSH.
type VirshClient struct {
	sshClient *ssh.Client
	// connURI is the local libvirt connection URI passed to virsh -c on the
	// remote host (qemu:///system or qemu:///session).
	connURI string
	// localMode is true when running against local libvirt (no SSH).
	localMode bool
	log       logr.Logger
}

// NewVirshClient creates a new VirshClient that executes virsh commands over
// SSH against the given local connection URI (ConnURISystem or
// ConnURISession). An empty connURI defaults to the system daemon.
func NewVirshClient(sshClient *ssh.Client, connURI string) *VirshClient {
	if connURI == "" {
		connURI = ConnURISystem
	}
	return &VirshClient{
		sshClient: sshClient,
		connURI:   connURI,
		log:       logr.Discard(),
	}
}

// NewLocalVirshClient creates a new VirshClient that executes virsh commands locally.
func NewLocalVirshClient() *VirshClient {
	return &VirshClient{
		connURI:   ConnURISystem,
		localMode: true,
		log:       logr.Discard(),
	}
}

// WithLogger sets the logger on the VirshClient and returns the client for chaining.
func (c *VirshClient) WithLogger(log logr.Logger) *VirshClient {
	c.log = log
	return c
}

func (c *VirshClient) runVirsh(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{"virsh", "-c", c.connURI}, args...)
	cmd := shellJoin(cmdArgs...)
	c.log.V(2).Info("Executing virsh command", "cmd", cmd)

	if c.localMode {
		// For local mode, would use exec.CommandContext.
		// Not implemented in Phase 1 — SSH is the primary path.
		return "", fmt.Errorf("local mode not implemented")
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Set deadline via context.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			stderrStr := strings.TrimSpace(stderr.String())
			c.log.V(1).Info("Virsh command failed", "cmd", args[0], "stderr", stderrStr)
			if stderrStr != "" {
				return "", ClassifyVirshError(stderrStr, args[0], getResourceArg(args))
			}
			return "", fmt.Errorf("virsh %s: %w", args[0], err)
		}
	}

	c.log.V(2).Info("Virsh command completed", "cmd", args[0], "stdout_len", len(stdout.String()))
	return strings.TrimSpace(stdout.String()), nil
}

// runSSH executes a shell command on the remote host via SSH.
func (c *VirshClient) runSSH(ctx context.Context, cmd string) error {
	stderr, err := c.execSSH(ctx, cmd)
	if err == nil {
		return nil
	}
	if stderr != "" {
		return fmt.Errorf("%s: %s", cmd, stderr)
	}
	return fmt.Errorf("%s: %w", cmd, err)
}

// execSSH executes a shell command on the remote host via SSH, returning the
// trimmed stderr alongside the run error so callers can build their own
// error messages.
func (c *VirshClient) execSSH(ctx context.Context, cmd string) (string, error) {
	c.log.V(2).Info("Executing SSH command", "cmd", cmd)
	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stderr bytes.Buffer
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return strings.TrimSpace(stderr.String()), ctx.Err()
	case err := <-done:
		if err != nil {
			stderrStr := strings.TrimSpace(stderr.String())
			c.log.V(1).Info("SSH command failed", "cmd", cmd, "stderr", stderrStr)
			return stderrStr, err
		}
	}
	return strings.TrimSpace(stderr.String()), nil
}

func getResourceArg(args []string) string {
	if len(args) > 1 {
		return args[len(args)-1]
	}
	return ""
}

// Ping checks connectivity to the libvirt host.
func (c *VirshClient) Ping(ctx context.Context) error {
	_, err := c.runVirsh(ctx, "version")
	return err
}

// VerifyHypervisor confirms the KVM hypervisor stack is installed and usable.
// CAPLV defines domains as <domain type='kvm'>, so it queries the KVM driver's
// domain capabilities directly: `virsh domcapabilities --virttype kvm` fails
// when the QEMU/KVM driver or emulator is absent (e.g. qemu-kvm /
// libvirt-daemon-driver-qemu not installed) or KVM is otherwise unavailable —
// a partial install that `virsh version`/`nodeinfo` would happily answer.
func (c *VirshClient) VerifyHypervisor(ctx context.Context) error {
	_, err := c.runVirsh(ctx, "domcapabilities", "--virttype", "kvm")
	return err
}

// VerifySessionPrerequisites confirms the host setup that session-mode
// (qemu:///session) machines depend on but that no virsh query covers:
//
//   - qemu-bridge-helper must be setuid root (or carry cap_net_admin) so the
//     unprivileged QEMU can attach taps to whitelisted bridges — without it
//     every VM start fails with an opaque bridge-helper error.
//   - loginctl lingering must be enabled for the service account, otherwise
//     systemd tears down /run/user/<uid> (and with it the session daemon and
//     its VMs) when the last SSH session closes — a failure that only shows
//     up after the controller disconnects.
//
// Both are configured by the setup-host.yaml playbook with
// libvirt_mode=session. The check scripts emit their own diagnostics on
// stderr, which become the returned error message.
func (c *VirshClient) VerifySessionPrerequisites(ctx context.Context) error {
	if c.localMode {
		return fmt.Errorf("local mode not implemented")
	}

	const helperCheck = `for h in /usr/libexec/qemu-bridge-helper /usr/lib/qemu/qemu-bridge-helper; do ` +
		`if [ -e "$h" ]; then ` +
		`if [ -u "$h" ] || getcap "$h" 2>/dev/null | grep -q cap_net_admin; then exit 0; fi; ` +
		`echo "$h is not setuid root and lacks cap_net_admin; session-mode VMs cannot attach to bridges" >&2; exit 1; ` +
		`fi; done; ` +
		`echo "qemu-bridge-helper not found; is qemu-kvm installed?" >&2; exit 1`
	if stderr, err := c.execSSH(ctx, helperCheck); err != nil {
		if stderr != "" {
			return fmt.Errorf("%s", stderr)
		}
		return fmt.Errorf("qemu-bridge-helper check: %w", err)
	}

	const lingerCheck = `u=$(id -un); ` +
		`if [ "$(loginctl show-user "$u" --property=Linger 2>/dev/null)" != "Linger=yes" ]; then ` +
		`echo "lingering is not enabled for $u — session VMs die when the last SSH session closes (run: loginctl enable-linger $u)" >&2; exit 1; ` +
		`fi`
	if stderr, err := c.execSSH(ctx, lingerCheck); err != nil {
		if stderr != "" {
			return fmt.Errorf("%s", stderr)
		}
		return fmt.Errorf("linger check: %w", err)
	}
	return nil
}

// GetNodeInfo returns host hardware information from virsh nodeinfo.
func (c *VirshClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	output, err := c.runVirsh(ctx, "nodeinfo")
	if err != nil {
		return nil, err
	}
	info := &NodeInfo{}
	var cores, threads, sockets int64
	for line := range strings.SplitSeq(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip units (e.g., "65390 KiB" -> "65390")
		val = strings.Fields(val)[0]
		switch key {
		case "CPU socket(s)":
			sockets, _ = strconv.ParseInt(val, 10, 32)
		case "Core(s) per socket":
			cores, _ = strconv.ParseInt(val, 10, 32)
		case "Thread(s) per core":
			threads, _ = strconv.ParseInt(val, 10, 32)
		case "Memory size":
			info.MemoryKB, _ = strconv.ParseInt(val, 10, 64)
		case "CPU(s)":
			// Total CPUs reported directly — use as fallback
			cpus, _ := strconv.ParseInt(val, 10, 32)
			info.CPUs = int32(cpus)
		}
	}
	// Prefer computed value if all components are available.
	if sockets > 0 && cores > 0 && threads > 0 {
		info.CPUs = int32(sockets * cores * threads)
	}
	return info, nil
}

// DomainExists returns true if a domain with the given name exists.
func (c *VirshClient) DomainExists(ctx context.Context, name string) (bool, error) {
	_, err := c.runVirsh(ctx, "dominfo", name)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetDomain retrieves information about a domain by name.
func (c *VirshClient) GetDomain(ctx context.Context, name string) (*DomainInfo, error) {
	output, err := c.runVirsh(ctx, "dominfo", name)
	if err != nil {
		return nil, err
	}
	info := &DomainInfo{Name: name}
	for line := range strings.SplitSeq(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "UUID":
			info.UUID = val
		case "State":
			info.State = val
		}
	}
	return info, nil
}

// DefineDomain defines a new domain from the given XML definition.
func (c *VirshClient) DefineDomain(ctx context.Context, xmlDef string) (*DomainInfo, error) {
	// Write XML to temp file on remote host, then virsh define it.
	tmpFile := fmt.Sprintf("/tmp/caplv-domain-%d.xml", time.Now().UnixNano())

	// Upload XML via SSH.
	session, err := c.sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	session.Stdin = strings.NewReader(xmlDef)
	if err := session.Run(fmt.Sprintf("cat > %s", shellQuote(tmpFile))); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("upload domain XML: %w", err)
	}
	_ = session.Close()

	// Define the domain.
	_, err = c.runVirsh(ctx, "define", tmpFile)
	if err != nil {
		return nil, err
	}

	// Clean up temp file via SSH.
	cleanSession, _ := c.sshClient.NewSession()
	if cleanSession != nil {
		_ = cleanSession.Run(fmt.Sprintf("rm -f %s", shellQuote(tmpFile)))
		_ = cleanSession.Close()
	}

	return c.GetDomain(ctx, extractDomainNameFromXML(xmlDef))
}

func extractDomainNameFromXML(xml string) string {
	// Simple extraction — look for <name>...</name>.
	start := strings.Index(xml, "<name>")
	end := strings.Index(xml, "</name>")
	if start >= 0 && end > start {
		return xml[start+6 : end]
	}
	return ""
}

// StartDomain starts a defined domain.
func (c *VirshClient) StartDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "start", name)
	return err
}

// DestroyDomain forcibly stops a running domain.
func (c *VirshClient) DestroyDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "destroy", name)
	if IsNotFound(err) {
		return nil // already off or gone
	}
	return err
}

// UndefineDomain removes a domain definition.
func (c *VirshClient) UndefineDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "undefine", name, "--nvram")
	if IsNotFound(err) {
		return nil
	}
	return err
}

// PoolExists returns true if a storage pool with the given name exists.
func (c *VirshClient) PoolExists(ctx context.Context, name string) (bool, error) {
	_, err := c.runVirsh(ctx, "pool-info", name)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PoolIsActive returns true if the storage pool exists and is running.
func (c *VirshClient) PoolIsActive(ctx context.Context, name string) (bool, error) {
	output, err := c.runVirsh(ctx, "pool-info", name)
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(output, "\n") {
		if state, ok := strings.CutPrefix(line, "State:"); ok {
			return strings.TrimSpace(state) == "running", nil
		}
	}
	return false, nil
}

// StartPool starts a defined but inactive storage pool.
func (c *VirshClient) StartPool(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "pool-start", name)
	return err
}

// CreateTmpfsPool creates a tmpfs-backed storage pool, mounts it, and starts it.
// Uses sudo for mkdir and mount (requires sudoers entry for the service account).
// If size is non-empty it is passed as the tmpfs `size=` option (e.g. "80%", "16G");
// when empty the kernel default (50% of physical RAM) applies.
func (c *VirshClient) CreateTmpfsPool(ctx context.Context, name, path, size string) error {
	// The helper creates the mount point and mounts the tmpfs in one step. It
	// owns the uid/gid/mode policy: a bare `mount -t tmpfs` leaves the mount
	// root world-writable (tmpfs defaults to 1777), and on session-mode hosts
	// the per-user daemon — which runs as the service account, not root —
	// needs to create and unlink volume files inside it.
	args := []string{path}
	if size != "" {
		args = append(args, size)
	}
	if err := c.runSSH(ctx, hostCtlCmd("mount-tmpfs", args...)); err != nil {
		return fmt.Errorf("tmpfs mount failed: %w", err)
	}

	// Define and start the pool (virsh — no sudo needed with libvirt group).
	if _, err := c.runVirsh(ctx, "pool-define-as", name, "dir", "--target", path); err != nil {
		return fmt.Errorf("pool-define-as failed: %w", err)
	}
	if _, err := c.runVirsh(ctx, "pool-start", name); err != nil {
		return fmt.Errorf("pool-start failed: %w", err)
	}
	return nil
}

// DestroyPool stops a storage pool, undefines it, and unmounts the backing tmpfs.
func (c *VirshClient) DestroyPool(ctx context.Context, name string) error {
	// Get the pool target path before destroying.
	output, err := c.runVirsh(ctx, "pool-dumpxml", name)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	// Extract <path>...</path> from pool XML.
	poolPath := extractXMLElement(output, "path")

	// Destroy (stop) and undefine the pool.
	_, _ = c.runVirsh(ctx, "pool-destroy", name)  // ignore errors — may already be stopped
	_, _ = c.runVirsh(ctx, "pool-undefine", name) // ignore errors — may already be undefined

	// Unmount tmpfs and remove the directory (the helper does both, and tolerates
	// an already-unmounted or already-removed path).
	if poolPath != "" {
		_ = c.runSSH(ctx, hostCtlCmd("umount", poolPath))
	}
	return nil
}

func extractXMLElement(xml, element string) string {
	start := strings.Index(xml, "<"+element+">")
	end := strings.Index(xml, "</"+element+">")
	if start >= 0 && end > start {
		return xml[start+len(element)+2 : end]
	}
	return ""
}

// VolumeExists returns true if a volume with the given name exists in the pool.
func (c *VirshClient) VolumeExists(ctx context.Context, pool, name string) (bool, error) {
	_, err := c.runVirsh(ctx, "vol-info", name, "--pool", pool)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateVolumeFromBackingStore creates a new qcow2 volume backed by an existing volume.
func (c *VirshClient) CreateVolumeFromBackingStore(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error {
	const bytesPerMB = 1024 * 1024
	sizeMB := sizeBytes / bytesPerMB
	_, err := c.runVirsh(ctx, "vol-create-as", pool, name,
		fmt.Sprintf("%dM", sizeMB),
		"--format", "qcow2",
		"--backing-vol-format", "qcow2",
		"--backing-vol", backingPath)
	return err
}

// CloneVolume clones an existing volume within the same pool.
func (c *VirshClient) CloneVolume(ctx context.Context, pool, sourceName, targetName string) error {
	_, err := c.runVirsh(ctx, "vol-clone", sourceName, targetName, "--pool", pool)
	return err
}

// CreateVolume creates a new qcow2 volume in the specified pool.
func (c *VirshClient) CreateVolume(ctx context.Context, pool, name string, sizeBytes int64) error {
	const bytesPerMB = 1024 * 1024
	sizeMB := sizeBytes / bytesPerMB
	_, err := c.runVirsh(ctx, "vol-create-as", pool, name, fmt.Sprintf("%dM", sizeMB), "--format", "qcow2")
	return err
}

// UploadVolumeFromBytes uploads raw bytes to a new volume in the specified pool.
func (c *VirshClient) UploadVolumeFromBytes(ctx context.Context, pool, name string, data []byte) error {
	tmpFile := fmt.Sprintf("/tmp/caplv-upload-%d", time.Now().UnixNano())

	// Upload data via SSH.
	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	session.Stdin = bytes.NewReader(data)
	if err := session.Run(fmt.Sprintf("cat > %s", shellQuote(tmpFile))); err != nil {
		_ = session.Close()
		return fmt.Errorf("upload data: %w", err)
	}
	_ = session.Close()

	// Create raw volume for the ISO.
	sizeBytes := int64(len(data))
	_, err = c.runVirsh(ctx, "vol-create-as", pool, name,
		fmt.Sprintf("%d", sizeBytes),
		"--format", "raw")
	if err != nil {
		return err
	}

	// Upload to the volume.
	_, err = c.runVirsh(ctx, "vol-upload", name, tmpFile, "--pool", pool)

	// Clean up temp file.
	cleanSession, _ := c.sshClient.NewSession()
	if cleanSession != nil {
		_ = cleanSession.Run(fmt.Sprintf("rm -f %s", shellQuote(tmpFile)))
		_ = cleanSession.Close()
	}

	return err
}

// UploadQcow2Volume streams a qcow2 from r into a new volume named `name`
// inside `pool`. Implementation: stream r to a temp file on the host via
// SSH `cat > <tmp>`, then `virsh vol-create-as` with the given virtual
// size and `--format qcow2`, then `virsh vol-upload`, then unlink the
// temp file. Atomicity: vol-upload is atomic from libvirt's perspective —
// the volume isn't visible to other operations until upload completes.
// On error the partial volume and temp file are cleaned up best-effort.
func (c *VirshClient) UploadQcow2Volume(ctx context.Context, pool, name string, r io.Reader, virtualSizeBytes int64) error {
	tmpFile := fmt.Sprintf("/tmp/caplv-upload-%d", time.Now().UnixNano())

	// Stream r into the temp file on the host.
	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	session.Stdin = r
	if err := session.Run(fmt.Sprintf("cat > %s", shellQuote(tmpFile))); err != nil {
		_ = session.Close()
		return fmt.Errorf("stream qcow2 to %s: %w", tmpFile, err)
	}
	_ = session.Close()

	cleanupTmp := func() {
		cs, _ := c.sshClient.NewSession()
		if cs != nil {
			_ = cs.Run(fmt.Sprintf("rm -f %s", shellQuote(tmpFile)))
			_ = cs.Close()
		}
	}

	// Create the libvirt-side volume of the qcow2's virtual size, then
	// upload the on-host file's bytes into it.
	if _, err := c.runVirsh(ctx, "vol-create-as", pool, name,
		fmt.Sprintf("%d", virtualSizeBytes),
		"--format", "qcow2"); err != nil {
		cleanupTmp()
		return fmt.Errorf("vol-create-as %s/%s: %w", pool, name, err)
	}
	if _, err := c.runVirsh(ctx, "vol-upload", name, tmpFile, "--pool", pool); err != nil {
		_, _ = c.runVirsh(ctx, "vol-delete", name, "--pool", pool)
		cleanupTmp()
		return fmt.Errorf("vol-upload %s/%s: %w", pool, name, err)
	}
	cleanupTmp()
	return nil
}

// DeleteVolume deletes a volume from the specified pool.
func (c *VirshClient) DeleteVolume(ctx context.Context, pool, name string) error {
	_, err := c.runVirsh(ctx, "vol-delete", name, "--pool", pool)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// GetVolumePath returns the filesystem path of a volume.
func (c *VirshClient) GetVolumePath(ctx context.Context, pool, name string) (string, error) {
	return c.runVirsh(ctx, "vol-path", name, "--pool", pool)
}

// hostCtl is the single privileged helper the service account may run under
// sudo. It owns the ownership and mode policy for everything CAPLV puts on a
// host, and confines every path to /run/caplv — see the "Privileged helper"
// task in deploy/ansible/setup-host.yaml for why the previous per-command
// sudoers rules could not do that safely.
const hostCtl = "/usr/local/sbin/caplv-hostctl"

// hostCtlCmd builds a `sudo caplv-hostctl <action> <args...>` command line.
// Arguments are still shell-quoted: the helper validates them, but nothing
// should depend on that alone.
func hostCtlCmd(action string, args ...string) string {
	return "sudo " + hostCtl + " " + action + " " + shellJoin(args...)
}

// WriteRemoteFile writes data to a file on the remote host via SSH.
//
// Both the directory and the file are created by the helper with an owner and
// mode that admit only the process that consumes them (qemu in system mode, the
// service account in session mode). They are deliberately not world-readable:
// the ignition config staged here carries the cluster's worker bootstrap
// credentials, and these hosts run incumbent workloads with other local users.
func (c *VirshClient) WriteRemoteFile(ctx context.Context, path string, data []byte) error {
	return c.WriteRemoteFileFrom(ctx, path, bytes.NewReader(data))
}

// WriteRemoteFileFrom writes what r yields to a file on the remote host via
// SSH, streaming it, as WriteRemoteFile does for bytes in memory.
func (c *VirshClient) WriteRemoteFileFrom(ctx context.Context, path string, r io.Reader) error {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return fmt.Errorf("remote path must be absolute and contain a directory: %q", path)
	}
	dir := path[:idx]

	if err := c.runSSH(ctx, hostCtlCmd("mkdir", dir)); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session for write: %w", err)
	}
	session.Stdin = r
	if err := session.Run(hostCtlCmd("write", path)); err != nil {
		_ = session.Close()
		return fmt.Errorf("write remote file %s: %w", path, err)
	}
	_ = session.Close()
	return nil
}

// RemoteFileExists returns true if path exists on the remote host. The check
// goes through the helper because the artifact directories are no longer
// world-traversable.
func (c *VirshClient) RemoteFileExists(ctx context.Context, path string) (bool, error) {
	session, err := c.sshClient.NewSession()
	if err != nil {
		return false, fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout bytes.Buffer
	session.Stdout = &stdout
	done := make(chan error, 1)
	go func() {
		done <- session.Run(hostCtlCmd("exists", path))
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return false, ctx.Err()
	case err := <-done:
		if err != nil {
			return false, fmt.Errorf("remote file check %s: %w", path, err)
		}
	}
	return strings.TrimSpace(stdout.String()) == "present", nil
}

// DeleteRemoteFile deletes a file on the remote host via SSH.
func (c *VirshClient) DeleteRemoteFile(ctx context.Context, path string) error {
	return c.runSSH(ctx, hostCtlCmd("rm", path))
}

// ReserveDHCPAddress makes the libvirt network hand ip to mac by DHCP, with
// hostname name, replacing an earlier reservation for mac. On a network
// without DHCP, libvirt adds a static-only DHCP range, so the network still
// hands out no address it was not told to.
func (c *VirshClient) ReserveDHCPAddress(ctx context.Context, network, mac, ip, name string) error {
	host := fmt.Sprintf("<host mac='%s' name='%s' ip='%s'/>", xmlAttr(mac), xmlAttr(name), xmlAttr(ip))
	add := func() error {
		_, err := c.runVirsh(ctx, "net-update", network, "add-last", "ip-dhcp-host", host, "--live", "--config")
		return err
	}
	err := add()
	if err != nil && strings.Contains(err.Error(), "existing dhcp host entry") {
		if err := c.ReleaseDHCPAddress(ctx, network, mac); err != nil {
			return err
		}
		err = add()
	}
	return err
}

// ReleaseDHCPAddress removes the libvirt network's DHCP reservation for mac;
// there being none is not an error.
func (c *VirshClient) ReleaseDHCPAddress(ctx context.Context, network, mac string) error {
	host := fmt.Sprintf("<host mac='%s'/>", xmlAttr(mac))
	_, err := c.runVirsh(ctx, "net-update", network, "delete", "ip-dhcp-host", host, "--live", "--config")
	if err != nil && strings.Contains(err.Error(), "couldn't locate a matching dhcp host entry") {
		return nil
	}
	return err
}

// xmlAttrEscaper escapes a string for an XML attribute value.
var xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", `"`, "&quot;")

// xmlAttr escapes s for an XML attribute value.
func xmlAttr(s string) string {
	return xmlAttrEscaper.Replace(s)
}

// RemoveRemoteDir removes an empty directory on the remote host via SSH.
func (c *VirshClient) RemoveRemoteDir(ctx context.Context, path string) error {
	return c.runSSH(ctx, hostCtlCmd("rmdir", path))
}

// Close closes the underlying SSH connection.
func (c *VirshClient) Close() error {
	if c.sshClient != nil {
		return c.sshClient.Close()
	}
	return nil
}

func shellJoin(args ...string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
