package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// gerberEntryNames is the real entry set observed from
// `eda.pcb_ManufactureData.getGerberFile('name')` on EasyEDA Pro desktop
// 3.2.149 (Windows 11, international build) — the evidence this feature was
// designed against. Keep it verbatim; it is the fixture, not an example.
var gerberEntryNames = []string{
	"Gerber_TopLayer.GTL",
	"Gerber_BottomLayer.GBL",
	"Gerber_TopSilkscreenLayer.GTO",
	"Gerber_BottomSilkscreenLayer.GBO",
	"Gerber_TopSolderMaskLayer.GTS",
	"Gerber_BottomSolderMaskLayer.GBS",
	"Gerber_TopPasteMaskLayer.GTP",
	"Gerber_BottomPasteMaskLayer.GBP",
	"Gerber_BoardOutlineLayer.GKO",
	"Drill_PTH_Through.DRL",
	"Drill_NPTH_Through.DRL",
	"Drill_PTH_Through_Via.DRL",
	"FlyingProbeTesting.json",
}

func TestClassifyGerberEntryCoversTheObservedArchive(t *testing.T) {
	want := map[string]string{
		"Gerber_TopLayer.GTL":              "copper",
		"Gerber_BottomLayer.GBL":           "copper",
		"Gerber_InnerLayer1.G1":            "copper",
		"Gerber_InnerLayer2.GP2":           "copper",
		"Gerber_TopSilkscreenLayer.GTO":    "silkscreen",
		"Gerber_BottomSilkscreenLayer.GBO": "silkscreen",
		"Gerber_TopSolderMaskLayer.GTS":    "soldermask",
		"Gerber_BottomSolderMaskLayer.GBS": "soldermask",
		"Gerber_TopPasteMaskLayer.GTP":     "paste",
		"Gerber_BottomPasteMaskLayer.GBP":  "paste",
		"Gerber_BoardOutlineLayer.GKO":     "outline",
		"Drill_PTH_Through.DRL":            "drill",
		"Drill_NPTH_Through.DRL":           "drill",
		"Drill_PTH_Through_Via.DRL":        "drill",
		"FlyingProbeTesting.json":          "report",
		// Name-based fallbacks for a build that renames an extension.
		"drill_map.txt":  "drill",
		"BoardOutline":   "outline",
		"README":         "other",
		"nested/dir.GTL": "copper",
	}
	for name, category := range want {
		if got := classifyGerberEntry(name); got != category {
			t.Errorf("classifyGerberEntry(%q) = %q, want %q", name, got, category)
		}
	}
}

func TestSummarizeGerberZipCountsLayersAndDrills(t *testing.T) {
	entries := make([]gerberZipEntry, 0, len(gerberEntryNames))
	for i, name := range gerberEntryNames {
		entries = append(entries, gerberZipEntry{Name: name, Size: int64(i + 1), Category: classifyGerberEntry(name)})
	}
	got := summarizeGerberZip(entries)

	if got.Count != 13 {
		t.Errorf("Count = %d, want 13", got.Count)
	}
	if got.TotalSize != 91 { // 1+2+…+13
		t.Errorf("TotalSize = %d, want 91", got.TotalSize)
	}
	if !got.HasOutline {
		t.Error("HasOutline = false; the fixture carries Gerber_BoardOutlineLayer.GKO")
	}
	wantCopper := []string{"Gerber_BottomLayer.GBL", "Gerber_TopLayer.GTL"} // sorted
	if !reflect.DeepEqual(got.Copper, wantCopper) {
		t.Errorf("Copper = %v, want %v", got.Copper, wantCopper)
	}
	if len(got.Drills) != 3 {
		t.Errorf("Drills = %v, want 3 entries", got.Drills)
	}
	for category, n := range map[string]int{"copper": 2, "silkscreen": 2, "soldermask": 2, "paste": 2, "outline": 1, "drill": 3, "report": 1} {
		if got.Counts[category] != n {
			t.Errorf("Counts[%q] = %d, want %d", category, got.Counts[category], n)
		}
	}
	if warnings := gerberZipWarnings(got); len(warnings) != 0 {
		t.Errorf("a complete archive must produce no warnings, got %v", warnings)
	}
}

func TestGerberZipWarningsNameEveryMissingFabPiece(t *testing.T) {
	empty := summarizeGerberZip(nil)
	warnings := gerberZipWarnings(empty)
	if len(warnings) != 3 {
		t.Fatalf("an empty archive must warn about copper, drills and outline; got %v", warnings)
	}
	joined := strings.Join(warnings, " ")
	for _, want := range []string{"铜层", "钻孔", "板框"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %v missing %q", warnings, want)
		}
	}

	// Copper + outline present, drills absent (a board with no holes at all, or
	// an export that dropped them) — exactly one warning, naming the drills.
	partial := summarizeGerberZip([]gerberZipEntry{
		{Name: "Gerber_TopLayer.GTL", Category: "copper"},
		{Name: "Gerber_BoardOutlineLayer.GKO", Category: "outline"},
	})
	if warnings := gerberZipWarnings(partial); len(warnings) != 1 || !strings.Contains(warnings[0], "钻孔") {
		t.Errorf("partial archive warnings = %v, want exactly the drill warning", warnings)
	}
}

func TestReadGerberZipListsRealArchiveEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Gerber.zip")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range gerberEntryNames {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte("G04 fixture*\n")); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A directory member must be skipped, not reported as a file.
	if _, err := w.Create("subdir/"); err != nil {
		t.Fatalf("create dir member: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}

	entries, err := readGerberZip(path)
	if err != nil {
		t.Fatalf("readGerberZip: %v", err)
	}
	if len(entries) != len(gerberEntryNames) {
		t.Fatalf("got %d entries, want %d (directory members must be skipped)", len(entries), len(gerberEntryNames))
	}
	summary := summarizeGerberZip(entries)
	if len(summary.Copper) != 2 || len(summary.Drills) != 3 || !summary.HasOutline {
		t.Fatalf("summary of the real archive = %+v", summary)
	}
	if summary.TotalSize != int64(len(gerberEntryNames)*len("G04 fixture*\n")) {
		t.Errorf("TotalSize = %d, want the sum of uncompressed sizes", summary.TotalSize)
	}

	var out bytes.Buffer
	printGerberZipSummary(&out, summary)
	text := out.String()
	for _, want := range []string{"Gerber_TopLayer.GTL", "Drill_NPTH_Through.DRL", "copper=2", "drill=3"} {
		if !strings.Contains(text, want) {
			t.Errorf("human listing missing %q:\n%s", want, text)
		}
	}
}

func TestReadGerberZipRejectsANonArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.zip")
	if err := os.WriteFile(path, []byte("this is not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readGerberZip(path); err == nil {
		t.Fatal("readGerberZip must fail on a non-archive so the caller can warn instead of printing a fake listing")
	}
}

func TestParseGerberDigits(t *testing.T) {
	got, err := parseGerberDigits("2.6")
	if err != nil {
		t.Fatalf("parseGerberDigits(2.6): %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"integerNumber": 2, "decimalNumber": 6}) {
		t.Errorf("parseGerberDigits(2.6) = %v", got)
	}
	if _, err := parseGerberDigits(" 3.5 "); err != nil {
		t.Errorf("surrounding whitespace must be tolerated: %v", err)
	}
	for _, bad := range []string{"", "26", "2.6.1", "a.6", "2.b", "0.6", "2.0", "-2.6"} {
		if _, err := parseGerberDigits(bad); err == nil {
			t.Errorf("parseGerberDigits(%q) must fail", bad)
		}
	}
}

func TestParse3DElements(t *testing.T) {
	got, err := parse3DElements("component model, Via ,Silkscreen")
	if err != nil {
		t.Fatalf("parse3DElements: %v", err)
	}
	// Case-insensitive input is normalized to the exact union member the API wants.
	want := []string{"Component Model", "Via", "Silkscreen"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parse3DElements = %v, want %v", got, want)
	}
	for _, bad := range []string{"", " , ", "Copper Pour", "Component Model,Pads"} {
		if _, err := parse3DElements(bad); err == nil {
			t.Errorf("parse3DElements(%q) must fail", bad)
		}
	}
}

// ── flag parsing / --help self-description ───────────────────────────

func TestPcbExportFabCommandsRejectUnsupportedUnits(t *testing.T) {
	cfg := &appConfig{host: defaultHost, ports: "60832-60841"}
	window := ""
	var out, errOut bytes.Buffer

	// A unit the API does not accept must be refused by the CLI itself — before
	// any daemon scan — so the failure is a flag error, not a network error.
	gerber := newPcbExportGerberCmd(cfg, &window, &out, &errOut)
	gerber.SetArgs([]string{"--unit", "mil"})
	gerber.SetOut(&out)
	gerber.SetErr(&errOut)
	gerber.SilenceUsage, gerber.SilenceErrors = true, true
	if err := gerber.Execute(); err == nil || !strings.Contains(err.Error(), "mm 或 inch") {
		t.Fatalf("export-gerber --unit mil error = %v, want a unit refusal", err)
	}

	pnp := newPcbExportPnpCmd(cfg, &window, &out, &errOut)
	pnp.SetArgs([]string{"--unit", "inch"})
	pnp.SetOut(&out)
	pnp.SetErr(&errOut)
	pnp.SilenceUsage, pnp.SilenceErrors = true, true
	if err := pnp.Execute(); err == nil || !strings.Contains(err.Error(), "mm 或 mil") {
		t.Fatalf("export-pnp --unit inch error = %v, want a unit refusal", err)
	}

	pnpType := newPcbExportPnpCmd(cfg, &window, &out, &errOut)
	pnpType.SetArgs([]string{"--type", "tsv"})
	pnpType.SetOut(&out)
	pnpType.SetErr(&errOut)
	pnpType.SilenceUsage, pnpType.SilenceErrors = true, true
	if err := pnpType.Execute(); err == nil || !strings.Contains(err.Error(), "csv 或 xlsx") {
		t.Fatalf("export-pnp --type tsv error = %v, want a type refusal", err)
	}

	model := newPcbExport3DCmd(cfg, &window, &out, &errOut)
	model.SetArgs([]string{"--mode", "outfit"})
	model.SetOut(&out)
	model.SetErr(&errOut)
	model.SilenceUsage, model.SilenceErrors = true, true
	if err := model.Execute(); err == nil || !strings.Contains(err.Error(), "Outfit") {
		t.Fatalf("export-3d --mode outfit error = %v, want a mode refusal", err)
	}
}

func TestPcbExportFabCommandsAreSelfDescribing(t *testing.T) {
	cfg := &appConfig{host: defaultHost, ports: "60832-60841"}
	window := ""
	var out, errOut bytes.Buffer

	cases := []struct {
		cmd   string
		build func() *cobra.Command
		flags []string
		want  []string
	}{
		{
			cmd:   "export-gerber",
			build: func() *cobra.Command { return newPcbExportGerberCmd(cfg, &window, &out, &errOut) },
			flags: []string{"name", "unit", "digits", "color-silkscreen", "out", "json"},
			want:  []string{"easyeda pcb export-gerber", "mm|inch"},
		},
		{
			cmd:   "export-pnp",
			build: func() *cobra.Command { return newPcbExportPnpCmd(cfg, &window, &out, &errOut) },
			flags: []string{"name", "type", "unit", "out", "json"},
			want:  []string{"easyeda pcb export-pnp", "UTF-16"},
		},
		{
			cmd:   "export-3d",
			build: func() *cobra.Command { return newPcbExport3DCmd(cfg, &window, &out, &errOut) },
			flags: []string{"name", "type", "mode", "elements", "auto-generate", "out", "json"},
			want:  []string{"easyeda pcb export-3d", "STEP"},
		},
	}
	for _, tc := range cases {
		c := tc.build()
		for _, flag := range tc.flags {
			if c.Flags().Lookup(flag) == nil {
				t.Errorf("%s: missing --%s", tc.cmd, flag)
			}
		}
		help := c.Long + "\n" + c.Example + "\n" + c.Short
		for _, want := range tc.want {
			if !strings.Contains(help, want) {
				t.Errorf("%s --help must mention %q; got:\n%s", tc.cmd, want, help)
			}
		}
		if c.Example == "" {
			t.Errorf("%s: --help must carry examples (CLI design rule 2)", tc.cmd)
		}
	}
}
