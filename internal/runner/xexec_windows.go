//go:build windows

package runner

// defaultShell is used when a script step leaves shell empty.
func (c *sXExecRunner) defaultShell() string {
	return "cmd"
}

func (c *sXExecRunner) selfScriptSuffix() string {
	switch c.shell {
	case "cmd", "bat":
		return ".bat"
	case "powershell", "ps", "ps1":
		return ".ps1"
	default:
		return ".bat"
	}
}
