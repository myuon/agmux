package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const plistLabel = "com.myuon.agmux"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{ .Label }}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{ .BinaryPath }}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{ .Path }}</string>
    </dict>
    <key>StandardOutPath</key>
    <string>{{ .StdoutLog }}</string>
    <key>StandardErrorPath</key>
    <string>{{ .StderrLog }}</string>
</dict>
</plist>
`

type plistData struct {
	Label      string
	BinaryPath string
	Path       string
	StdoutLog  string
	StderrLog  string
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux"), nil
}

// Install generates the launchd plist and loads the agent.
func Install() error {
	// Use the currently running binary so the plist always references
	// the installed agmux that the user actually invoked.
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine current executable path: %w", err)
	}
	// Resolve symlinks to get the real path
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	logd, err := logDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logd, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	data := plistData{
		Label:      plistLabel,
		BinaryPath: binPath,
		Path:       os.Getenv("PATH"),
		StdoutLog:  filepath.Join(logd, "server.log"),
		StderrLog:  filepath.Join(logd, "agmux.log"),
	}

	ppath, err := plistPath()
	if err != nil {
		return err
	}

	// Ensure LaunchAgents directory exists
	if err := os.MkdirAll(filepath.Dir(ppath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	f, err := os.Create(ppath)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	defer f.Close()

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	// Load the agent
	if out, err := exec.Command("launchctl", "load", ppath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %s: %w", string(out), err)
	}

	fmt.Printf("Installed and loaded %s\n", ppath)
	fmt.Printf("Binary: %s\n", binPath)
	fmt.Printf("Logs:   %s/server.log, %s/agmux.log\n", logd, logd)
	return nil
}

// Uninstall unloads the agent and removes the plist file.
func Uninstall() error {
	ppath, err := plistPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(ppath); os.IsNotExist(err) {
		return fmt.Errorf("plist not found at %s; nothing to uninstall", ppath)
	}

	// Unload the agent (ignore errors if not loaded)
	_ = exec.Command("launchctl", "unload", ppath).Run()

	if err := os.Remove(ppath); err != nil {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Printf("Uninstalled %s\n", ppath)
	return nil
}
