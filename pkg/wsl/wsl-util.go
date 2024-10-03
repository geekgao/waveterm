// Copyright 2024, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wsl

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func DetectShell(ctx context.Context, client *Distro) (string, error) {
	wshPath := GetWshPath(ctx, client)

	cmd := client.WslCommand(ctx, wshPath+" shell")
	log.Printf("shell detecting using command: %s shell", wshPath)
	out, err := cmd.Output()
	if err != nil {
		log.Printf("unable to determine shell. defaulting to /bin/bash: %s", err)
		return "/bin/bash", nil
	}
	log.Printf("detecting shell: %s", out)

	// quoting breaks this particular case
	return strings.TrimSpace(string(out)), nil
}

func GetWshVersion(ctx context.Context, client *Distro) (string, error) {
	wshPath := GetWshPath(ctx, client)

	cmd := client.WslCommand(ctx, wshPath+" version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func GetWshPath(ctx context.Context, client *Distro) string {
	defaultPath := "~/.waveterm/bin/wsh"

	cmd := client.WslCommand(ctx, "which wsh")
	out, whichErr := cmd.Output()
	if whichErr == nil {
		return strings.TrimSpace(string(out))
	}

	cmd = client.WslCommand(ctx, "where.exe wsh")
	out, whereErr := cmd.Output()
	if whereErr == nil {
		return strings.TrimSpace(string(out))
	}

	// check cmd on windows since it requires an absolute path with backslashes
	cmd = client.WslCommand(ctx, "(dir 2>&1 *``|echo %userprofile%\\.waveterm%\\.waveterm\\bin\\wsh.exe);&<# rem #>echo none")
	out, cmdErr := cmd.Output() //todo
	if cmdErr == nil && strings.TrimSpace(string(out)) != "none" {
		return strings.TrimSpace(string(out))
	}

	// no custom install, use default path
	return defaultPath
}

func hasBashInstalled(ctx context.Context, client *Distro) (bool, error) {
	cmd := client.WslCommand(ctx, "which bash")
	out, whichErr := cmd.Output()
	if whichErr == nil && len(out) != 0 {
		return true, nil
	}

	cmd = client.WslCommand(ctx, "where.exe bash")
	out, whereErr := cmd.Output()
	if whereErr == nil && len(out) != 0 {
		return true, nil
	}

	// note: we could also check in /bin/bash explicitly
	// just in case that wasn't added to the path. but if
	// that's true, we will most likely have worse
	// problems going forward

	return false, nil
}

func GetClientOs(ctx context.Context, client *Distro) (string, error) {
	cmd := client.WslCommand(ctx, "uname -s")
	out, unixErr := cmd.Output()
	if unixErr == nil {
		formatted := strings.ToLower(string(out))
		formatted = strings.TrimSpace(formatted)
		return formatted, nil
	}

	cmd = client.WslCommand(ctx, "echo %OS%")
	out, cmdErr := cmd.Output()
	if cmdErr == nil && strings.TrimSpace(string(out)) != "%OS%" {
		formatted := strings.ToLower(string(out))
		formatted = strings.TrimSpace(formatted)
		return strings.Split(formatted, "_")[0], nil
	}

	cmd = client.WslCommand(ctx, "echo $env:OS")
	out, psErr := cmd.Output()
	if psErr == nil && strings.TrimSpace(string(out)) != "$env:OS" {
		formatted := strings.ToLower(string(out))
		formatted = strings.TrimSpace(formatted)
		return strings.Split(formatted, "_")[0], nil
	}
	return "", fmt.Errorf("unable to determine os: {unix: %s, cmd: %s, powershell: %s}", unixErr, cmdErr, psErr)
}

func GetClientArch(ctx context.Context, client *Distro) (string, error) {
	cmd := client.WslCommand(ctx, "uname -m")
	out, unixErr := cmd.Output()
	if unixErr == nil {
		formatted := strings.ToLower(string(out))
		formatted = strings.TrimSpace(formatted)
		if formatted == "x86_64" {
			return "x64", nil
		}
		return formatted, nil
	}

	cmd = client.WslCommand(ctx, "echo %PROCESSOR_ARCHITECTURE%")
	out, cmdErr := cmd.Output()
	if cmdErr == nil && strings.TrimSpace(string(out)) != "%PROCESSOR_ARCHITECTURE%" {
		formatted := strings.ToLower(string(out))
		return strings.TrimSpace(formatted), nil
	}

	cmd = client.WslCommand(ctx, "echo $env:PROCESSOR_ARCHITECTURE")
	out, psErr := cmd.Output()
	if psErr == nil && strings.TrimSpace(string(out)) != "$env:PROCESSOR_ARCHITECTURE" {
		formatted := strings.ToLower(string(out))
		return strings.TrimSpace(formatted), nil
	}
	return "", fmt.Errorf("unable to determine architecture: {unix: %s, cmd: %s, powershell: %s}", unixErr, cmdErr, psErr)
}

var installTemplateRawBash = `bash -c ' \
mkdir -p {{.installDir}}; \
cat > {{.tempPath}}; \
mv {{.tempPath}} {{.installPath}}; \
chmod a+x {{.installPath}};' \
`

var installTemplateRawDefault = ` \
mkdir -p {{.installDir}}; \
cat > {{.tempPath}}; \
mv {{.tempPath}} {{.installPath}}; \
chmod a+x {{.installPath}}; \
`

func CpHostToRemote(ctx context.Context, client *Distro, sourcePath string, destPath string) error {
	// warning: does not work on windows remote yet
	bashInstalled, err := hasBashInstalled(ctx, client)
	if err != nil {
		return err
	}

	var selectedTemplateRaw string
	if bashInstalled {
		selectedTemplateRaw = installTemplateRawBash
	} else {
		log.Printf("bash is not installed on remote. attempting with default shell")
		selectedTemplateRaw = installTemplateRawDefault
	}

	// I need to use toSlash here to force unix keybindings
	// this means we can't guarantee it will work on a remote windows machine
	var installWords = map[string]string{
		"installDir":  filepath.ToSlash(filepath.Dir(destPath)),
		"tempPath":    destPath + ".temp",
		"installPath": destPath,
	}

	installCmd := &bytes.Buffer{}
	installTemplate := template.Must(template.New("").Parse(selectedTemplateRaw))
	installTemplate.Execute(installCmd, installWords)

	cmd := client.WslCommand(ctx, installCmd.String())
	installStdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	input, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("cannot open local file %s to send to host: %v", sourcePath, err)
	}

	go func() {
		io.Copy(installStdin, input)
		// don't need this?
		process := cmd.GetProcess()
		process.Kill()
	}()

	cmd.Wait()
	// this is bad error handling, but i have to do this
	// because of the Process.Kill()
	// this should be replaced with another way to get
	// passed the Wait
	return nil
}

func InstallClientRcFiles(ctx context.Context, client *Distro) error {
	path := GetWshPath(ctx, client)
	log.Printf("path to wsh searched is: %s", path)

	cmd := client.WslCommand(ctx, path+" rcfiles")
	_, err := cmd.Output()
	return err
}

func GetHomeDir(ctx context.Context, client *Distro) string {
	// note: also works for powershell
	cmd := client.WslCommand(ctx, `echo "$HOME"`)
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}

	cmd = client.WslCommand(ctx, `echo %userprofile%`)
	out, err = cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}

	return "~"
}

func IsPowershell(shellPath string) bool {
	// get the base path, and then check contains
	shellBase := filepath.Base(shellPath)
	return strings.Contains(shellBase, "powershell") || strings.Contains(shellBase, "pwsh")
}
