package status

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// Golden-file tests: the text output for each captured sample is kept in testdata/golden so that any change to
// the layout shows up in review. Run `go test ./internal/status -update` after an intended change.
func TestRenderGolden(t *testing.T) {
	cases := []struct{ set, name string }{
		{fixtureVersion, "sklearn-iris"},
		{fixtureVersion, "bad-storage"},
		{fixtureVersion, "no-runtime"},
		{fixtureGatewayAPI, "sklearn-iris"},
		{fixtureGatewayAPI, "sklearn-canary"},
	}
	for _, tc := range cases {
		name := tc.name
		if tc.set != fixtureVersion {
			name = tc.set + "-" + tc.name
		}
		t.Run(name, func(t *testing.T) {
			f := loadFixtureFrom(t, tc.set, tc.name)
			s := f.snapshot(t)
			findings := Diagnose(s)
			for i := range findings {
				if findings[i].Logs != nil {
					findings[i].LogLines = []string{"(log lines are fetched from the cluster)"}
				}
			}
			sum := Summarize(s, findings)

			var buf bytes.Buffer
			r := TextRenderer{Now: sum.Created.Add(57 * time.Minute)}
			if err := r.Render(&buf, sum); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join("testdata", "golden", name+".txt")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("output differs from %s:\n--- got ---\n%s\n--- want ---\n%s", path, buf.String(), want)
			}
		})
	}
}
