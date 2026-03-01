package setup

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed plist.tmpl
var plistTemplateStr string

const (
	// BinaryName is the name of the installed binary.
	BinaryName = "reminderrelay"

	// InstallDir is the default install directory for the binary.
	InstallDir = "/usr/local/bin"

	// PlistLabel is the launchd job label.
	PlistLabel = "com.github.njoerd114.reminderrelay"
)

// plistData holds template values for the launchd plist.
type plistData struct {
	BinaryPath string
	HomeDir    string
}

// BinaryInstallPath returns the full path to the installed binary.
func BinaryInstallPath() string {
	return filepath.Join(InstallDir, BinaryName)
}

// PlistPath returns the launchd plist destination path.
func PlistPath(homeDir string) string {
	return filepath.Join(homeDir, "Library", "LaunchAgents", PlistLabel+".plist")
}

// LogDir returns the log directory path.
func LogDir(homeDir string) string {
	return filepath.Join(homeDir, "Library", "Logs", BinaryName)
}

// InstallBinary copies the currently-running binary to /usr/local/bin.
// Uses sudo if the target directory is not writable by the current user.
func InstallBinary() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving current executable path: %w", err)
	}

	// Resolve symlinks so we copy the actual binary.
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolving executable symlinks: %w", err)
	}

	dest := BinaryInstallPath()

	// Check if the target directory is writable.
	if isWritable(InstallDir) {
		return copyFile(self, dest, 0o755)
	}

	// Fallback: use sudo to install.
	//nolint:gosec // sudo is intentional here — user is prompted by macOS.
	cmd := exec.Command("sudo", "install", "-m", "755", self, dest)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo install to %s: %w", dest, err)
	}
	return nil
}

// WritePlist renders the launchd plist from the embedded template and writes
// it to ~/Library/LaunchAgents/.
func WritePlist(homeDir string) error {
	tmpl, err := template.New("plist").Parse(plistTemplateStr)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	data := plistData{
		BinaryPath: BinaryInstallPath(),
		HomeDir:    homeDir,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("executing plist template: %w", err)
	}

	dest := PlistPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}

	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing plist to %s: %w", dest, err)
	}
	return nil
}

// CreateLogDir creates the ~/Library/Logs/reminderrelay/ directory.
func CreateLogDir(homeDir string) error {
	dir := LogDir(homeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating log directory %s: %w", dir, err)
	}
	return nil
}

// LoadDaemon loads the launchd plist so the daemon starts immediately.
// If already loaded, it is unloaded first.
func LoadDaemon(homeDir string) error {
	plist := PlistPath(homeDir)
	_ = UnloadDaemon(homeDir) // ignore error if not loaded
	//nolint:gosec // user-controlled path
	cmd := exec.Command("launchctl", "load", plist)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// UnloadDaemon unloads the launchd plist (stops the daemon).
func UnloadDaemon(homeDir string) error {
	plist := PlistPath(homeDir)
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		return nil // nothing to unload
	}
	//nolint:gosec // user-controlled path
	cmd := exec.Command("launchctl", "unload", plist)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl unload: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// RemovePlist deletes the launchd plist file.
func RemovePlist(homeDir string) error {
	plist := PlistPath(homeDir)
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist %s: %w", plist, err)
	}
	return nil
}

// RemoveBinary deletes the installed binary from /usr/local/bin.
// Uses sudo if the directory is not writable.
func RemoveBinary() error {
	path := BinaryInstallPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // already gone
	}

	if isWritable(InstallDir) {
		return os.Remove(path)
	}

	//nolint:gosec // sudo is intentional
	cmd := exec.Command("sudo", "rm", "-f", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// IsDaemonLoaded checks whether the launchd job is currently loaded.
func IsDaemonLoaded() bool {
	cmd := exec.Command("launchctl", "list", PlistLabel)
	return cmd.Run() == nil
}

// PurgeUserData removes config, state database, and log files.
func PurgeUserData(homeDir string) error {
	dirs := []string{
		filepath.Join(homeDir, ".config", BinaryName),
		filepath.Join(homeDir, ".local", "share", BinaryName),
		LogDir(homeDir),
	}
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing %s: %w", dir, err)
		}
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

// isWritable checks if the given directory is writable by the current user.
func isWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".rr-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// copyFile copies src to dst with the given permissions.
func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return nil
}
