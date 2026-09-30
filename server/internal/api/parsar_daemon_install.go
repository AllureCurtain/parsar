package api

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

//go:embed install_parsar_daemon.sh
var installParsarDaemonScript string

//go:embed install_parsar_daemon.ps1
var installParsarDaemonPowerShellScript string

type ParsarDaemonInstallConfig struct {
	// Repo is the GitHub `owner/name` slug whose Releases the script
	// downloads from. Empty keeps the script's built-in placeholder.
	Repo string
}

// RegisterParsarDaemonInstallRoute serves the Bash and PowerShell installers
// verbatim. Unauthenticated: the scripts contain no secrets and only pull
// public daemon artifacts.
func RegisterParsarDaemonInstallRoute(r chi.Router, cfg ParsarDaemonInstallConfig) {
	shellScript := renderParsarDaemonInstallScript(installParsarDaemonScript, cfg.Repo)
	powerShellScript := renderParsarDaemonInstallScript(installParsarDaemonPowerShellScript, cfg.Repo)
	r.Get("/api/v1/parsar-daemon/install.sh", parsarDaemonInstallHandler(shellScript))
	r.Get("/api/v1/parsar-daemon/install.ps1", parsarDaemonPowerShellInstallHandler(powerShellScript))
}

func renderParsarDaemonInstallScript(script, configuredRepo string) string {
	repo := strings.TrimSpace(configuredRepo)
	if repo == "" {
		return script
	}
	// Replace only the DEFAULT_REPO assignment, not the case-pattern
	// marker that gates the fallback. A blanket ReplaceAll would
	// rewrite both and re-trigger the fallback path.
	if strings.Contains(script, `DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`) {
		return strings.Replace(script,
			`DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`,
			fmt.Sprintf(`DEFAULT_REPO=%q`, repo),
			1)
	}
	return strings.Replace(script,
		`$defaultRepo = '__PARSAR_DAEMON_REPO__'`,
		fmt.Sprintf(`$defaultRepo = '%s'`, strings.ReplaceAll(repo, "'", "''")),
		1)
}

// parsarDaemonInstallHandler serves the rendered install script body.
// Extracted so swag can attach annotations to a named function.
//
//	@Summary	Serve parsar-daemon install script
//	@Description	Returns the shell script that fetches and installs the parsar-daemon binary. Unauthenticated — the script itself has no secrets.
//	@Tags		runtimes
//	@ID			installParsarDaemonScript
//	@Produce	plain
//	@Success	200 {string} string "install script body"
//	@Router		/api/v1/parsar-daemon/install.sh [get]
func parsarDaemonInstallHandler(script string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(script))
	}
}

// parsarDaemonPowerShellInstallHandler serves the Windows installer body.
//
//	@Summary	Serve parsar-daemon PowerShell install script
//	@Description	Returns the PowerShell script that fetches and installs the Windows x64 parsar-daemon binary. Unauthenticated — the script itself has no secrets.
//	@Tags		runtimes
//	@ID		installParsarDaemonPowerShellScript
//	@Produce	plain
//	@Success	200 {string} string "PowerShell install script body"
//	@Router		/api/v1/parsar-daemon/install.ps1 [get]
func parsarDaemonPowerShellInstallHandler(script string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-powershell; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(script))
	}
}
