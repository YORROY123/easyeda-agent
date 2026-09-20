package app

import (
	"archive/zip"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// 制造资料导出 (Gerber / 坐标文件 / 3D)
//
// 三条命令都是 READ-ONLY 的 typed action 包装 —— 在这之前用户只能走
// `debug exec` 裸调 eda.pcb_ManufactureData.*，而仓库规则是 typed-only。
//
// 文件走的是既有的 artifact 通道（连接器 inlineBase64 → daemon 落到
// `.easyeda/artifacts/`），和 `pcb export-dsn` / `pcb snapshot` / `bom export`
// 完全一致，没有新传输层。`--out` 只是把 daemon 已经落好的那份复制到指定路径。
//
// 输出约定跟随 `pcb check` / `pcb layout-lint`：默认人类可读摘要，`--json`
// 输出 `{ok,result}` 信封。Gerber 额外用纯 Go `archive/zip` 列出压缩包条目，
// 这样 Agent 不开 viewer 也能核对层和钻孔文件是否齐全（离线可测）。

// ── Gerber ZIP 检视（纯函数，离线可测）────────────────────────────────

// gerberZipEntry is one file inside an exported Gerber archive.
type gerberZipEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Category is the coarse role we could infer from the entry name:
	// copper / silkscreen / soldermask / paste / outline / drill / report / other.
	Category string `json:"category"`
}

// gerberZipSummary is the machine-readable digest printed after a Gerber export
// (and embedded in `--json`), so an Agent can answer "are all my layers and
// drill files in there?" without opening a CAM viewer.
type gerberZipSummary struct {
	Count      int              `json:"count"`
	TotalSize  int64            `json:"totalSize"`
	Counts     map[string]int   `json:"counts"`
	Copper     []string         `json:"copperLayers"`
	Drills     []string         `json:"drillFiles"`
	HasOutline bool             `json:"hasOutline"`
	Entries    []gerberZipEntry `json:"entries"`
}

// gerberCategoryOrder is the display/report order of the categories, chosen to
// read like a fab checklist (copper → silk → mask → paste → outline → drill).
var gerberCategoryOrder = []string{"copper", "silkscreen", "soldermask", "paste", "outline", "drill", "report", "other"}

// classifyGerberEntry infers an entry's role from its file name.
//
// Extension first (the RS-274X/Excellon convention EasyEDA emits — .GTL/.GBL,
// .G1…/.GP1…, .GTO/.GBO, .GTS/.GBS, .GTP/.GBP, .GKO, .DRL), then the
// descriptive name EasyEDA prefixes ("Gerber_TopLayer", "Drill_PTH_Through", …)
// as a fallback, so a build that renames an extension still classifies.
// Unknown entries are reported as "other" rather than guessed at.
func classifyGerberEntry(name string) string {
	base := name
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	lower := strings.ToLower(base)
	ext := ""
	if i := strings.LastIndex(lower, "."); i >= 0 {
		ext = lower[i+1:]
	}

	switch ext {
	case "drl", "txt", "xln", "tap":
		if ext != "txt" || strings.Contains(lower, "drill") {
			return "drill"
		}
	case "gko", "gm1", "gml":
		return "outline"
	case "gtl", "gbl":
		return "copper"
	case "gto", "gbo":
		return "silkscreen"
	case "gts", "gbs":
		return "soldermask"
	case "gtp", "gbp":
		return "paste"
	case "json":
		return "report"
	}
	// Inner copper layers: .G1 / .G2 / … (and .GP1 / .GP2 for plane layers).
	if strings.HasPrefix(ext, "gp") {
		if _, err := strconv.Atoi(ext[2:]); err == nil {
			return "copper"
		}
	}
	if strings.HasPrefix(ext, "g") && len(ext) > 1 {
		if _, err := strconv.Atoi(ext[1:]); err == nil {
			return "copper"
		}
	}

	switch {
	case strings.Contains(lower, "drill"):
		return "drill"
	case strings.Contains(lower, "boardoutline"), strings.Contains(lower, "outline"):
		return "outline"
	case strings.Contains(lower, "silkscreen"), strings.Contains(lower, "silk"):
		return "silkscreen"
	case strings.Contains(lower, "soldermask"), strings.Contains(lower, "mask"):
		return "soldermask"
	case strings.Contains(lower, "paste"):
		return "paste"
	case strings.Contains(lower, "toplayer"), strings.Contains(lower, "bottomlayer"),
		strings.Contains(lower, "innerlayer"), strings.Contains(lower, "signallayer"):
		return "copper"
	}
	return "other"
}

// summarizeGerberZip folds a list of entries into the reported digest. Pure —
// the unit tests drive it directly, no archive needed.
func summarizeGerberZip(entries []gerberZipEntry) gerberZipSummary {
	summary := gerberZipSummary{
		Count:   len(entries),
		Counts:  map[string]int{},
		Copper:  []string{},
		Drills:  []string{},
		Entries: entries,
	}
	for _, e := range entries {
		summary.TotalSize += e.Size
		summary.Counts[e.Category]++
		switch e.Category {
		case "copper":
			summary.Copper = append(summary.Copper, e.Name)
		case "drill":
			summary.Drills = append(summary.Drills, e.Name)
		case "outline":
			summary.HasOutline = true
		}
	}
	sort.Strings(summary.Copper)
	sort.Strings(summary.Drills)
	return summary
}

// readGerberZip opens the saved artifact and returns its classified entries.
// Directory members are skipped. Any error is the caller's to treat as
// non-fatal: the export itself already succeeded, only the listing failed.
func readGerberZip(path string) ([]gerberZipEntry, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open gerber zip %s: %w", path, err)
	}
	defer func() { _ = r.Close() }()

	entries := make([]gerberZipEntry, 0, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		entries = append(entries, gerberZipEntry{
			Name:     f.Name,
			Size:     int64(f.UncompressedSize64),
			Category: classifyGerberEntry(f.Name),
		})
	}
	return entries, nil
}

// gerberZipWarnings names the fab-critical pieces the archive is missing, so an
// obviously incomplete export is visible in the terminal instead of at the fab.
func gerberZipWarnings(s gerberZipSummary) []string {
	var warnings []string
	if len(s.Copper) == 0 {
		warnings = append(warnings, "没有识别出铜层文件（GTL/GBL/G1…）")
	}
	if len(s.Drills) == 0 {
		warnings = append(warnings, "没有钻孔文件（DRL）—— 板上若有过孔/插件孔，这是异常")
	}
	if !s.HasOutline {
		warnings = append(warnings, "没有板框文件（GKO）—— 先用 `easyeda pcb outline-get` 确认板框存在")
	}
	return warnings
}

// printGerberZipSummary renders the entry table a human (or an Agent reading
// the terminal) uses to sanity-check layers + drills.
func printGerberZipSummary(w io.Writer, s gerberZipSummary) {
	fmt.Fprintf(w, "ZIP 条目 %d 个，解压后合计 %d bytes\n", s.Count, s.TotalSize)
	var parts []string
	for _, c := range gerberCategoryOrder {
		if n := s.Counts[c]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", c, n))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "分类: %s\n", strings.Join(parts, "  "))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  FILE\tCATEGORY\tBYTES")
	for _, e := range s.Entries {
		fmt.Fprintf(tw, "  %s\t%s\t%d\n", e.Name, e.Category, e.Size)
	}
	_ = tw.Flush()
}

// ── 通用导出渲染 ──────────────────────────────────────────────────────

// firstArtifactPath returns the persisted path of the response's first artifact.
func firstArtifactPath(res *actionResult) string {
	if res == nil {
		return ""
	}
	for _, a := range res.Artifacts {
		if a.Path != "" {
			return a.Path
		}
	}
	return ""
}

// resultInt reads a JSON number field (they decode as float64) as an int64.
func resultInt(m map[string]any, key string) int64 {
	if f, ok := m[key].(float64); ok {
		return int64(f)
	}
	return 0
}

// printExportHeadline is the shared human line every fab export prints: what was
// exported, where it landed, how big it is. The 📎 prefix matches the artifact
// line the streaming commands emit, so the path stays greppable either way.
func printExportHeadline(w io.Writer, label string, res *actionResult) {
	path := firstArtifactPath(res)
	name := strField(res.Result, "fileName")
	size := resultInt(res.Result, "size")
	if path == "" {
		fmt.Fprintf(w, "✓ %s 已导出: %s (%d bytes) —— daemon 未回传落盘路径，用 --out 指定输出位置\n", label, name, size)
		return
	}
	fmt.Fprintf(w, "✓ %s 已导出 %s (%d bytes)\n📎 artifact saved: %s\n", label, name, size, path)
}

// ── easyeda pcb export-gerber ────────────────────────────────────────

func newPcbExportGerberCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var fileName, unit, digits, out string
	var colorSilk, asJSON bool

	c := &cobra.Command{
		Use:   "export-gerber",
		Short: "导出当前 PCB 的 Gerber 制版文件（ZIP 制造资料）",
		Args:  cobra.NoArgs,
		Long: `导出当前 PCB 的 Gerber 制版文件（` + "`eda.pcb_ManufactureData.getGerberFile`" + `，@beta）。

只读 —— 不改动画布，不触发 autosave，也不会把后续读取标脏。产物是一个 ZIP，
含各铜层 (GTL/GBL/内层)、丝印、阻焊、锡膏、板框 (GKO) 和钻孔文件 (DRL)。

导出后本命令用纯 Go 解析 ZIP 目录并列出条目（文件名 / 分类 / 解压后字节数），
Agent 不必打开 CAM viewer 就能核对层和钻孔是否齐全；缺铜层 / 缺钻孔 / 缺板框
会额外给出警告。层与导出对象保持平台（嘉立创生产）默认，本命令不替 fab 改选层。

单位只接受 mm|inch（mil 是坐标文件那一组的单位，不是 Gerber 的）。`,
		Example: `  easyeda pcb export-gerber
  easyeda pcb export-gerber --name MyBoard_Gerber --unit mm
  easyeda pcb export-gerber --digits 2.6 --color-silkscreen
  easyeda pcb export-gerber --out build/Gerber.zip
  easyeda pcb export-gerber --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			if fileName != "" {
				payload["fileName"] = fileName
			}
			if unit != "" {
				u := strings.ToLower(unit)
				if u != "mm" && u != "inch" {
					return fmt.Errorf("--unit 只接受 mm 或 inch（mil 只用于 export-pnp），收到 %q", unit)
				}
				payload["unit"] = u
			}
			if digits != "" {
				format, err := parseGerberDigits(digits)
				if err != nil {
					return err
				}
				payload["digitalFormat"] = format
			}
			if cmd.Flags().Changed("color-silkscreen") {
				payload["colorSilkscreen"] = colorSilk
			}

			res, err := requestAction(cfg, "pcb.export.gerber", *window, payload)
			if err != nil {
				return err
			}

			path := firstArtifactPath(res)
			var summary gerberZipSummary
			var listErr error
			if path != "" {
				entries, err := readGerberZip(path)
				if err != nil {
					listErr = err
				} else {
					summary = summarizeGerberZip(entries)
				}
			}

			if asJSON {
				report := map[string]any{}
				for k, v := range res.Result {
					report[k] = v
				}
				report["path"] = path
				if listErr == nil && path != "" {
					report["zip"] = summary
					report["zipWarnings"] = gerberZipWarnings(summary)
				} else if listErr != nil {
					report["zipError"] = listErr.Error()
				}
				if err := encodeResultEnvelope(res, report, stdout); err != nil {
					return err
				}
			} else {
				printExportHeadline(stdout, "Gerber", res)
				switch {
				case listErr != nil:
					fmt.Fprintf(stderr, "warning: 导出成功但无法解析 ZIP 目录: %v\n", listErr)
				case path != "":
					printGerberZipSummary(stdout, summary)
					for _, w := range gerberZipWarnings(summary) {
						fmt.Fprintf(stderr, "⚠ %s\n", w)
					}
				}
			}

			if out != "" {
				return saveFirstArtifact(res, out, stderr)
			}
			return nil
		},
	}
	c.Flags().StringVar(&fileName, "name", "", "Gerber 文件名（默认 Gerber）")
	c.Flags().StringVar(&unit, "unit", "", "坐标单位: mm | inch（默认跟随平台）")
	c.Flags().StringVar(&digits, "digits", "", `坐标数字格式 "整数位.小数位"，例如 2.6`)
	c.Flags().BoolVar(&colorSilk, "color-silkscreen", false, "生成嘉立创彩色丝印制造文件")
	c.Flags().StringVarP(&out, "out", "o", "", "另存一份到该路径")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 {ok,result} 信封（含 zip 条目清单）")
	return c
}

// parseGerberDigits parses the `--digits I.D` coordinate format into the
// `{integerNumber, decimalNumber}` shape getGerberFile documents. Both halves
// must be positive integers; anything else is a flag error the CLI refuses
// before dispatching (nothing is exported).
func parseGerberDigits(raw string) (map[string]any, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf(`--digits 需要 "整数位.小数位" 形式，例如 2.6（收到 %q）`, raw)
	}
	intPart, err := strconv.Atoi(parts[0])
	if err != nil || intPart <= 0 {
		return nil, fmt.Errorf(`--digits 的整数位必须是正整数（收到 %q）`, parts[0])
	}
	decPart, err := strconv.Atoi(parts[1])
	if err != nil || decPart <= 0 {
		return nil, fmt.Errorf(`--digits 的小数位必须是正整数（收到 %q）`, parts[1])
	}
	return map[string]any{"integerNumber": intPart, "decimalNumber": decPart}, nil
}

// ── easyeda pcb export-pnp ───────────────────────────────────────────

func newPcbExportPnpCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var fileName, fileType, unit, out string
	var asJSON bool

	c := &cobra.Command{
		Use:   "export-pnp",
		Short: "导出当前 PCB 的坐标文件（贴片 Pick & Place）",
		Args:  cobra.NoArgs,
		Long: `导出当前 PCB 的坐标文件 / 贴片位置文件
（` + "`eda.pcb_ManufactureData.getPickAndPlaceFile`" + `，@beta）。只读。

⚠ 已在 EasyEDA Pro 桌面版 3.2.149 实测：` + "`--type csv`" + ` 得到的其实是
**UTF-16 编码、TAB 分隔**的文本（名字叫 csv 而已）。下游解析必须按 UTF-16 解码并按
TAB 切分，不能当 UTF-8 逗号 CSV 读；响应里的 ` + "`encodingCaveat`" + ` 会重复这条提醒。
要确定性的表格就用 ` + "`--type xlsx`" + `。

单位只接受 mm|mil（inch 是 Gerber 那一组的单位，不是坐标文件的）。`,
		Example: `  easyeda pcb export-pnp
  easyeda pcb export-pnp --type xlsx --unit mm
  easyeda pcb export-pnp --name PickAndPlace --unit mil --out build/pnp.csv
  easyeda pcb export-pnp --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			if fileName != "" {
				payload["fileName"] = fileName
			}
			if fileType != "" {
				t := strings.ToLower(fileType)
				if t != "csv" && t != "xlsx" {
					return fmt.Errorf("--type 只接受 csv 或 xlsx，收到 %q", fileType)
				}
				payload["fileType"] = t
			}
			if unit != "" {
				u := strings.ToLower(unit)
				if u != "mm" && u != "mil" {
					return fmt.Errorf("--unit 只接受 mm 或 mil（inch 只用于 export-gerber），收到 %q", unit)
				}
				payload["unit"] = u
			}

			res, err := requestAction(cfg, "pcb.export.pick_and_place", *window, payload)
			if err != nil {
				return err
			}
			if asJSON {
				report := map[string]any{}
				for k, v := range res.Result {
					report[k] = v
				}
				report["path"] = firstArtifactPath(res)
				if err := encodeResultEnvelope(res, report, stdout); err != nil {
					return err
				}
			} else {
				printExportHeadline(stdout, "坐标文件", res)
				if caveat := strField(res.Result, "encodingCaveat"); caveat != "" {
					fmt.Fprintf(stderr, "⚠ %s\n", caveat)
				}
			}
			if out != "" {
				return saveFirstArtifact(res, out, stderr)
			}
			return nil
		},
	}
	c.Flags().StringVar(&fileName, "name", "", "坐标文件名（默认 PickAndPlace）")
	c.Flags().StringVar(&fileType, "type", "", "输出类型: csv | xlsx（默认 csv）")
	c.Flags().StringVar(&unit, "unit", "", "坐标单位: mm | mil（默认跟随平台）")
	c.Flags().StringVarP(&out, "out", "o", "", "另存一份到该路径")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 {ok,result} 信封")
	return c
}

// ── easyeda pcb export-3d ────────────────────────────────────────────

func newPcbExport3DCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var fileName, fileType, modelMode, elements, out string
	var autoGenerate, asJSON bool

	c := &cobra.Command{
		Use:   "export-3d",
		Short: "导出当前 PCB 的 3D 模型（STEP / OBJ）",
		Args:  cobra.NoArgs,
		Long: `导出当前 PCB 的 3D 模型文件（` + "`eda.pcb_ManufactureData.get3DFile`" + `，@beta）。只读。

上游明确的限制原样转达：**只有以 STEP 格式导入的元件模型**才会出现在导出的 STEP 里；
没绑 3D 模型的元件可以用 ` + "`--auto-generate`" + ` 按其"高度"属性生成方块占位。

本命令尚未在新构建上实机验证（@beta 接口，平台可能直接返回 undefined）；
真返回 undefined 时会得到一条点名该 API 的 typed 错误，而不是空文件。`,
		Example: `  easyeda pcb export-3d
  easyeda pcb export-3d --type obj --mode Parts
  easyeda pcb export-3d --elements "Component Model,Via,Silkscreen" --auto-generate
  easyeda pcb export-3d --out build/board.step --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			if fileName != "" {
				payload["fileName"] = fileName
			}
			if fileType != "" {
				t := strings.ToLower(fileType)
				if t != "step" && t != "obj" {
					return fmt.Errorf("--type 只接受 step 或 obj，收到 %q", fileType)
				}
				payload["fileType"] = t
			}
			if modelMode != "" {
				if modelMode != "Outfit" && modelMode != "Parts" {
					return fmt.Errorf("--mode 只接受 Outfit（装配体）或 Parts（零件），收到 %q", modelMode)
				}
				payload["modelMode"] = modelMode
			}
			if elements != "" {
				list, err := parse3DElements(elements)
				if err != nil {
					return err
				}
				payload["element"] = list
			}
			if cmd.Flags().Changed("auto-generate") {
				payload["autoGenerateModels"] = autoGenerate
			}

			res, err := requestAction(cfg, "pcb.export.model3d", *window, payload)
			if err != nil {
				return err
			}
			if asJSON {
				report := map[string]any{}
				for k, v := range res.Result {
					report[k] = v
				}
				report["path"] = firstArtifactPath(res)
				if err := encodeResultEnvelope(res, report, stdout); err != nil {
					return err
				}
			} else {
				printExportHeadline(stdout, "3D 模型", res)
			}
			if out != "" {
				return saveFirstArtifact(res, out, stderr)
			}
			return nil
		},
	}
	c.Flags().StringVar(&fileName, "name", "", "3D 文件名（默认 Model3D）")
	c.Flags().StringVar(&fileType, "type", "", "输出类型: step | obj（默认 step）")
	c.Flags().StringVar(&modelMode, "mode", "", "Outfit（装配体）| Parts（零件）")
	c.Flags().StringVar(&elements, "elements", "", `导出对象，逗号分隔: "Component Model,Via,Silkscreen,Wire In Signal Layer"`)
	c.Flags().BoolVar(&autoGenerate, "auto-generate", false, "为未绑 3D 模型的元件按高度属性自动生成模型")
	c.Flags().StringVarP(&out, "out", "o", "", "另存一份到该路径")
	c.Flags().BoolVar(&asJSON, "json", false, "输出 {ok,result} 信封")
	return c
}

// valid3DElements mirrors get3DFile's documented `element` union exactly.
var valid3DElements = []string{"Component Model", "Via", "Silkscreen", "Wire In Signal Layer"}

// parse3DElements splits and validates the comma-separated `--elements` list
// against get3DFile's union, refusing an unknown member before dispatch (the
// platform would otherwise silently drop it).
func parse3DElements(raw string) ([]string, error) {
	var list []string
	for _, part := range strings.Split(raw, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		ok := false
		for _, valid := range valid3DElements {
			if strings.EqualFold(item, valid) {
				list = append(list, valid)
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("--elements 含未知对象 %q，只接受: %s", item, strings.Join(valid3DElements, " | "))
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("--elements 不能为空")
	}
	return list, nil
}
