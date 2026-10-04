package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCodexDeltasAreBatchedAtActivityAndTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, s := range []string{"Hel", "lo", " world"} {
			fmt.Fprintf(w, "event: codex.delta\ndata: %s\n\n", s)
		}
		fmt.Fprint(w, "event: codex.activity\ndata: Using bash\n\n")
		for _, s := range []string{"fi", "nished"} {
			fmt.Fprintf(w, "event: codex.delta\ndata: %s\n\n", s)
		}
		fmt.Fprint(w, "event: done\ndata: {\"status\":\"completed\",\"summary\":\"finished\"}\n\n")
	}))
	defer srv.Close()
	var got []string
	completed := false
	watchEvents(context.Background(), "task", srv.URL, "", func(_, m string) { got = append(got, m) }, nil, func(s string, e error) {
		completed = true
		if e != nil {
			t.Fatal(e)
		}
	})
	if !completed || !reflect.DeepEqual(got, []string{"codex: Hello world", "codex: Using bash", "codex: finished"}) {
		t.Fatal(got, completed)
	}
}
func TestCodexDeltasFlushOnUnexpectedEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "event: codex.delta\ndata: partial\n\n") }))
	defer srv.Close()
	var got []string
	watchEvents(context.Background(), "task", srv.URL, "", func(_, m string) { got = append(got, m) }, nil, nil)
	if !reflect.DeepEqual(got, []string{"codex: partial"}) {
		t.Fatal(got)
	}
}
