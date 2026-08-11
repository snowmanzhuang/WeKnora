package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// duckdbExtensions is the list of DuckDB extensions required by WeKnora's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

// DuckDB's built-in core repository URL still uses HTTP. Some Docker
// networks reject or proxy plain HTTP downloads, while the same official
// repository is available over HTTPS.
const duckdbExtensionRepository = "https://extensions.duckdb.org"

const duckdbExtensionDownloadAttempts = 5

func downloadExtensions() {
	ctx := context.Background()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	for _, ext := range duckdbExtensions {
		var installErr error
		for attempt := 1; attempt <= duckdbExtensionDownloadAttempts; attempt++ {
			_, installErr = sqlDB.ExecContext(ctx, fmt.Sprintf("FORCE INSTALL %s FROM '%s';", ext, duckdbExtensionRepository))
			if installErr == nil {
				break
			}
			if attempt < duckdbExtensionDownloadAttempts {
				time.Sleep(time.Duration(attempt) * time.Second)
			}
		}
		if installErr != nil {
			panic(fmt.Errorf("failed to install %s extension after %d attempts: %w", ext, duckdbExtensionDownloadAttempts, installErr))
		}
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			panic(fmt.Errorf("failed to load %s extension: %w", ext, err))
		}
	}
}

func main() {
	downloadExtensions()
}
