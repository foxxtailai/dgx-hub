package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dgx-hub/internal/api"
	"dgx-hub/internal/platform"
	"dgx-hub/internal/storage"
	"dgx-hub/internal/telemetry"
	"dgx-hub/internal/tunnels"
	"dgx-hub/web"
)

var version = "0.1.2"

func main() {
	if err := run(); err != nil {
		log.Print(err)
		platform.ShowError("DGX Hub could not start", err.Error()+"\n\nCheck the DGX Hub log in the selected data directory for details.")
		os.Exit(1)
	}
}
func run() error {
	return runWithDesktop(os.Args[1:], desktopIntegration{openBrowser: platform.OpenBrowser, runTray: platform.RunTray})
}

func runWithDesktop(args []string, desktop desktopIntegration) (runErr error) {
	flags := flag.NewFlagSet("dgx-hub", flag.ContinueOnError)
	port := flags.Int("port", 8787, "local dashboard port (127.0.0.1 only)")
	dataDir := flags.String("data-dir", "", "override the per-user data directory")
	noTray := flags.Bool("no-tray", false, "run without tray integration or automatic browser opening for development")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	address := fmt.Sprintf("127.0.0.1:%d", *port)
	var opener func(string) error
	if !*noTray {
		opener = desktop.openBrowser
	}
	listener, err := listenOrOpen(context.Background(), address, opener)
	if err != nil || listener == nil {
		return err
	}
	defer listener.Close()
	// A repeated desktop launch finishes above, before touching logs, storage,
	// migrations, SSH operations or a second tray icon.
	useDefaultDataDir := *dataDir == ""
	if useDefaultDataDir {
		dir, err := platform.DataDir()
		if err != nil {
			return err
		}
		*dataDir = dir
	}
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(*dataDir, "dgx-hub.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			log.Printf("DGX Hub error: %v", runErr)
		}
		log.SetOutput(os.Stderr)
		_ = logFile.Close()
	}()
	log.SetOutput(logFile)
	{
		legacyDir := ""
		if !useDefaultDataDir {
			legacyDir = *dataDir
		}
		legacy, legacyErr := platform.LegacyDatabasePath(legacyDir)
		if legacyErr != nil {
			return legacyErr
		}
		imported, importErr := storage.ImportLegacyDatabase(context.Background(), legacy, filepath.Join(*dataDir, "dgx-hub.db"))
		if importErr != nil {
			return fmt.Errorf("import previous settings: %w", importErr)
		}
		if imported {
			log.Print("Imported previous settings into DGX Hub; original database preserved")
		}
	}
	store, err := storage.Open(filepath.Join(*dataDir, "dgx-hub.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	assets, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	manager := tunnels.New()
	defer manager.Close()
	app := api.New(store, telemetry.New(), manager, address, assets, version)
	defer app.Close()
	app.Start(ctx)
	server := &http.Server{Handler: app, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err != http.ErrServerClosed {
			serverErrors <- err
			cancel()
		}
	}()
	log.Printf("DGX Hub %s listening at http://%s", version, address)
	if !*noTray {
		if browserErr := desktop.openBrowser("http://" + address + "/"); browserErr != nil {
			log.Printf("open dashboard in browser: %v; open http://%s/ manually or retry from the tray", browserErr, address)
		}
	}
	if *noTray {
		<-ctx.Done()
	} else if err = desktop.runTray(ctx, "http://"+address, cancel); err != nil {
		log.Printf("tray error: %v", err)
		cancel()
	}
	cancel()
	app.Close()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
		_ = server.Close()
	}
	app.Wait()
	_ = manager.Close()
	log.Print("DGX Hub stopped")
	select {
	case serverErr := <-serverErrors:
		return serverErr
	default:
		return err
	}
}
