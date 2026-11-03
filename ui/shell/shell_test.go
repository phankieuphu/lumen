package shell

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMinLevelDropsDebugEvenWhenAppLogsDebug(t *testing.T) {
	var buf bytes.Buffer
	app := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := slog.New(minLevel{h: app, min: slog.LevelWarn}).With("component", "wails")

	log.Debug("Binding call complete", "args", `[{"password":"hunter2"}]`)
	log.Info("info")
	log.Warn("warned")

	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "info") {
		t.Fatalf("records below Warn were logged:\n%s", out)
	}
	if !strings.Contains(out, "warned") || !strings.Contains(out, "component=wails") {
		t.Fatalf("Warn record or attrs missing:\n%s", out)
	}
}

func TestAssetHandler(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"built UI is served", fstest.MapFS{"index.html": {Data: []byte("<p>app</p>")}}, "<p>app</p>"},
		{"missing build shows help", fstest.MapFS{".gitkeep": {}}, "make app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			assetHandler(tt.fs).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body %q does not contain %q", rec.Body.String(), tt.want)
			}
		})
	}
}
