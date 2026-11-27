//go:build !windows

package runner

// defaultShell is used when a script step leaves shell empty.
func (c *sXExecRunner) defaultShell() string {
	return "sh"
}

func (c *sXExecRunner) selfScriptSuffix() string {
	switch c.shell {
	case "ash":
		return ".ash"
	case "bash":
		return ".bash"
	case "csh":
		return ".csh"
	case "dash":
		return ".dash"
	case "ksh":
		return ".ksh"
	case "shell", "sh":
		return ".sh"
	case "tcsh":
		return ".tcsh"
	case "zsh":
		return ".zsh"
	default:
		return ".bash"
	}
}
