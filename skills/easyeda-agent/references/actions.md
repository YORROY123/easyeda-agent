# CLI 与 Action 使用参考

`easyeda <command> --help` 是命令签名，`easyeda actions` 是机器可读 typed action 目录。
本文件只保留调用边界；不复制全部命令和历史修复。原理图主流程见
[schematic-data.md](schematic-data.md) 与 [auto-layout-sop.md](auto-layout-sop.md)。

## 1.4 原理图入口

| 目的 | CLI |
|---|---|
| 获取 canonical 图 | `sch connectivity`；全工程用 `--all-pages` 逐页读取 |
| 离线比较连接 | `sch connectivity-diff <before.json> <after.json>` |
| 修复功能式位号 | `sch designators allocate` → `plan` → `sch apply` |
| 完整本地版本比较 | `sch design-diff expected.json actual.json --exit-code`；检查 coverage/unverified |
| 由测量计算 Lib 内部 | `sch lib-layout --from layout-input.json --out composition.json`；纯离线 |
| 核心相对移动/单脚标签修复 | `sch layout-edit --source zones.json --page page.json --snapshot fresh.json (--move-core ID --to X,Y \| --repair-pin ID:PIN) --out target.json [--report report.json] [--playbook repair.json]`；纯离线生成，修复 playbook 使用作用域 action |
| 合并已设计的 Lib 几何 | `sch compose --from … --out … --before … --playbook …` |
| 放置固定 IR 中的器件 | `sch materialize <connectivity.json> --out …`；不是完整布局/布线器 |
| 少量显式标记连接增量 | `sch plan <before.json> <after.json>`；不支持任意器件或导线 diff |
| 转换/核验模块方框与标题 | `sch frame apply/check --from …` |
| 执行计划 | `sch apply <playbook.json>` |

生成器的输入、支持范围与位号/身份规则集中在 [schematic-data.md](schematic-data.md)。
数据层校验用于发现结构问题，执行时的实时回读用于证明变更确已生效。

compose 生成的全工程位号唯一性步骤使用 `schematic.components.list` 的
`allPages:true,tagPages:true` 最小清单，只检查位号冲突、既有 primitiveId 和待建位号不存在；
它不请求慢速 device identity、bbox 或 pins。紧随其后的目标页守卫仍读取完整
device identity、bbox、pins、wires 与连接摘要，不能用前者替代后者。

## SCH Apply

```bash
easyeda sch apply steps.json --dry-run
easyeda sch apply steps.json --yes
```

`--dry-run` 只预检并打印，不执行步骤；`--yes` 对当前任务已授权的计划跳过交互提示。
不以文档中的示例替代用户授权，也不为已经授权的每个步骤重复请求确认。

Playbook 使用 `version:1`、`meta` 和有序 `steps`。每步只选一种执行方式：

- `action` + `payload`：typed action；输入按目录 schema 校验。
- `run` + `flags`/`args`：Cobra 子命令，例如 `sch frame apply`。
- `notify`：编辑器提示。

`capture:{"part":"$.primitiveId"}` 捕获新实例 ID，后续 payload 用 `${part}`；
不要把旧文件中的 primitive ID 当作新建结果。`assert` 的路径相对 action 的 `result`，
支持 `exists`、`true/false`、`==/!=`、数值比较及 `len` 比较。内部 run 继承目标工程/页。

执行默认失败即停，只读步骤可重试；变更超时不自动重发。`partial:true` 或非空
`notApplied` 会使 typed 步骤失败。成功回执不等于保存；计划应包含读回与显式 save。
可选 `verify` 是失败后的落地检查，不是事务回滚。已生效的前序步骤保留在画布和 journal。

**生成的保护计划**（如 compose、designators、connectivity plan）禁止改目标、
`--resume` 和 `--from/--to`。失败后重新读取当前图，修正输入，再完整编译/执行。
普通手写 playbook 支持这些选项，但恢复依赖原文件 SHA 和有效的捕获变量；修改文件后
不能继续使用旧 journal。只有明确验证过的独立片段才适合区间执行。

`audit export --playbook` 可从审计生成复现用队列；它可能包含 clear/delete 等操作，
也可能引用在录制区间外创建的 ID。先检查生成内容和 raw-id 警告，不能将录制物当作幂等模板。

## 常用原理图适配器边界

| CLI / action | 必要边界 |
|---|---|
| `doc ls/switch/open`，`document.current/open` | 使用工程和页面目标；同名页用 UUID。CLI 同时核对活动 UUID 与对象枚举 settle；只出现目标标签、但对象仍不可读时失败并要求停止写入、修复 typed reload/open 后复测 |
| `sch list`，`schematic.components.list` | `includeDeviceIdentity` 为重放解析真正库 UUID；`includePins/BBox/Wires` 取得几何基线。非激活页可能是浅数据 |
| `sch place`，`schematic.component.place` | 使用库 UUID；自动回填可确定的 C 号与空属性是 best-effort，须检查警告。没有 place 自定义属性输入契约 |
| `sch modify`，`schematic.component.modify` | `otherProperty`/`customAttributes` 二选一，合并保留原属性。`verified:false` 需要再回读，不能当已验证 |
| `sch prim-delete/clear` | 删除后按 ID 或完整图元清单验证；默认保护 sheet。未知枚举或幸存图元不能报告清空 |
| `sch connect/autoconnect`，`schematic.power.connect_pin` | 必须生成非零短线，flag 不能与 pin 重叠；connect 非幂等，autoconnect 可跳过已连接目标网 |
| `sch disconnect`，`schematic.pin.disconnect` | 检查共享树的 `alsoDisconnectedPins` 和删除残留；逐个恢复受影响引脚 |
| `sch no-connect` | 显式设置/清除 NC，不创建零长线，不推断缺失数据为 NC |
| `sch replace/rebind-symbol/rebind-footprint` | 会重建实例，重新读 ID；换器件查看 pinDiff，按引脚差异重连和验收 |
| `sch export-image` | 文档渲染 SVG/PNG/PDF；`--ids` 导局部，不依赖视口截图 |
| `sch read/check/bridge-check/drc/gate` | 用法与判读见 [schematic.md](schematic.md)；SDK DRC 聚合值不代表 UI 所有警告消失 |
| `sch save` | 通过阶段验证后保存并确认 `saved:true`，不能只依赖防抖 autosave |

`replace` 保留 sch↔PCB 的 `uniqueId`，器件型号/供应商字段随新 device；`--keep-properties`
才保留旧自定义属性。`rebind` 对不可写系统库可克隆到个人库；失败恢复仍需看实际回读。
修改属性时不要整包带入库的 `Designator` 等投影键。回放 `propertiesBefore` 只能恢复旧值，
无法通过 merge 删除新加的键。网络文件用 `sch_ManufactureData.getNetlistFile()`，
不用已废弃的 `sch_Netlist.getNetlist()`。

产物路径从 `artifacts[].path` 获取。`sch read/list` 本身直接输出 JSON；
`sch check --json` 使用 `{ok,result}` 信封，问题在 `result.findings`。
`bom export --type csv` 默认 best-effort 补 LCSC C 号，`--enrich=false` 可关闭，xlsx 不补。
需显式指定脚本时用 `--script`，安装态也可设置 `EASYEDA_SKILLS_DIR` 指向 Skill 的父目录。
补号解释器按 `python3` → `python` → `py -3` 依次探测（Windows 上会真正运行一次候选，
所以微软商店那个只会退出 9009 的 `python3.exe` 假入口会被跳过），需要钉死某个解释器
（venv、指定小版本）时设 `EASYEDA_PYTHON=/abs/path/to/python`；设了但不可执行直接报错，
不会退回其它解释器。找不到任何 Python 3 时只是补号失败并打 warning，导出的 BOM 仍然成立。

## 图纸与明细表

`sch titleblock-get` 先取得实际字段名；`sch titleblock --data` 只传要改的明细项，按
`--doc` 钉住聚焦页。不要把 get 返回的整包字段写回，尤其 Device/Symbol、几何与 `@` 投影项。
连接器按字段回读：unknownKeys 应修正键名，partial/notApplied 应检查实际状态，不能盲重试。
明细表接口不能设置纸张尺寸；换图框是独立的器件替换工作，不能用 Width/Height 伪装。

`page-new/rename/delete` 管单页，`sch rename` 管原理图文档。compose 不隐式删除源页；
删页应先确认目标器件/网络已经迁移，平台无程序化 undo。

## 器件库与自建资产

优先标准器件或 `lib by-lcsc` 的精确 C 号匹配。搜索结果须核对型号与封装，不能默认取第一条。
`sch resolve-lcsc` 只在型号和封装精确匹配时写回，unresolved 必须继续处理。

需要自建时按 `lib libraries` 找目标库，再用 `lib device build --spec device.json`
编排 Symbol、Footprint、可选 3D Model 与 Device；也可分步 create/build/get。
完整规格先运行 `lib device validate --spec device.json`，它离线核对 PDF 证据、几何字段、
重复编号以及 symbol pin ↔ footprint pad 集合；`device build` 会再次执行同一输入校验，防止写入非法资产。
PDF 通读、封装变体消歧和规格格式见 [library-authoring.md](library-authoring.md)。
资产使用可复用的 `EA_AGENT__<ASSET>` 命名，项目来源写属性或描述。create/build 的
`verified/partial/rollback` 必须核对；删除要求 UUID、library 和 expected-name 精确匹配。
Symbol/Footprint build 仅允许写入可证明为空的刚创建资产：Connector 在任何 create 前回读
目标 editor 的完整受支持图元 inventory，非空或读取不完整都以 `PRECONDITION_REFUSED`
零写入拒绝。build 不是追加或替换接口，不要重放同一 UUID；当前没有 `--replace`。

- Footprint JSON 的单位是 mil，pad/hole 使用官方 tuple；复杂弧线/区域优先用
  `lib footprint copy` 保留几何。层与制造规则见 [pcb.md](pcb.md)。
- `lib symbol build` 从轮廓、引脚与可选圆形生成符号；引脚编号、Pin-1 和极性需验证。
- 当前没有 Device rename typed action；实测官方 `lib_Device.modify` 改名返回 false 且不落地，
  不要用 `debug exec` 反复试探。需要新名称时新建并重新绑定 Device。
- `lib model3d search/copy/create` 获取模型；`lib device model3d` 绑定或清除，须回读
  模型 UUID 与 library UUID。`device create` 也支持模型绑定参数。
- 库 API 有 beta 能力；错误或结果不明时先 get，不能因即时读回缺失重复创建。

需要底层方法时先 `easyeda api search <query>`。typed action 尚缺的行为可临时探测，
验证后再实现 CLI；不把重复 debug 脚本积累成生产流程。

`debug exec` 的脚本编译失败返回 `PRECONDITION_REFUSED`，说明代码未执行：修正语法与
命令行转义后再提交，不原样重试。执行阶段抛错仍按 `EDA_CALL_FAILED` 处理，即使异常名为
SyntaxError；执行可能已经产生修改，必须回读。语法拒绝不计入连接器健康度。

队列拒绝只有在入队探针仍未返回、且近期旁路 `document.current` 成功时才使用
`CONNECTOR_QUEUE_BLOCKED`，CLI 可有界等待。旁路结果未知、过期或失败时返回
`CONNECTOR_HEALTH_UNVERIFIED`，停止自动等待，先切前台并检查旁路读取；持续不响应时
按恢复流程重启并回读。两种拒绝均未派发当前动作，不代表此前超时的写入没有落地。

布局路径的 `connect_pin` 与 `sch connect/autoconnect` 共用 35 秒请求预算，包含 daemon 的
2 秒回执余量。该预算不保证宿主一定完成；超时仍须回读，不能自动认定创建失败并重发。

`sch place` 为 daemon 等待连接器保留 8 秒，另外预留 2 秒传回结构化错误（请求共10秒）。
超时提示同时覆盖 HTTP 超时和 daemon 返回的 deadline 错误；先回读是否已经放置，再检查
库 UUID、窗口状态。超时不能单独证明 UUID 错误，也不能作为再次放置的依据。

## 外部工程导入边界

Altium Designer `.SchDoc` / `.PcbDoc` 当前没有可用的 typed action。官方 beta
`sys_FileManager.importProjectByProjectFile` 在已报告的 3.2.149 本地工作区会静默返回
`undefined` 且不产生工程副作用，不能包装后当成功。`sys_FormatConversion` 的 Altium
入口只适用于 `.SchLib` / `.PcbLib` 库转换。工程迁移当前标为 `unsupported`；不得通过
EasyEDA 交互界面兜底。能力边界与未来 typed 验收见 [project-import.md](project-import.md)。

## PCB 基础上下文（非穷举）

- `pcb.config.get` / `pcb.config.set` — 当前 PCB 的配置读取与参数化局部修改。CLI 为
  `pcb config get/clearance/track/via/bind`；参数、mil/mm、dry-run、部分成功及回读契约见
  [pcb-config.md](pcb-config.md)。`get` 导出可交给 `pcb drc-rules-set --from` 完整恢复。

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件（可含 pads）
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数（会先激活 PCB tab 保证 `currentLayer` 可读回；无当前层时附带 `visibleLayers` 作为显示状态证据）→ `easyeda pcb layers`
- `pcb.layers.set_current` — 切换当前编辑层（`--layer` 接受 id|层名|top|bottom|inner1）→ `easyeda pcb layer-set --layer bottom`
- `pcb.layers.visibility` — 显示/隐藏/聚焦层做视觉 QA：`--preset top-only|bottom-only|copper-only|silk-only`，或 `--show/--hide`（可加 `--exclusive` 只留所选）→ `easyeda pcb layer-visibility --preset bottom-only`
- `pcb.view.side` — 切到顶面/底面视图（选该面铜层为当前层 + 聚焦该面铜+丝印），随后 `pcb snapshot` 即反映该面。注意：EasyEDA 无原生画布翻面 API，这是「层聚焦」近似而非物理翻板 → `easyeda pcb view-side --side bottom`
- `pcb.nets.list` — PCB 全部网络

### 制造资料导出（只读，产物是 artifact）

三条 typed action 封装 `eda.pcb_ManufactureData.*`（均 `@beta`），替掉此前只能走
`debug exec` 的做法。都不改画布、不触发 autosave、不打脏标记；文件走既有 artifact 通道
落到 `.easyeda/artifacts/`，路径取 `artifacts[].path`。CLI 默认人类可读摘要，`--json` 出
`{ok,result}` 信封，`--out` 另存一份到指定路径。

- `pcb.export.gerber` — Gerber 制版文件（ZIP）。`unit` 只接受 **mm|inch**；另有
  `colorSilkscreen`、`digitalFormat{integerNumber,decimalNumber}`。导出层/对象保持平台默认。
  → `easyeda pcb export-gerber [--name N] [--unit mm|inch] [--digits 2.6] [--color-silkscreen]`，
  保存后**列出 ZIP 全部条目并按铜层/丝印/阻焊/锡膏/板框/钻孔分类计数**；缺铜层、缺钻孔或缺板框单独告警。
- `pcb.export.pick_and_place` — 坐标文件。`fileType` csv|xlsx（默认 csv），`unit` 只接受 **mm|mil**。
  ⚠️ 3.2.149 实测 csv 实为 **UTF-16 + TAB 分隔**，须按 UTF-16 解码并按 TAB 切分；结果里的
  `encodingCaveat` 会重复这条。→ `easyeda pcb export-pnp [--type csv|xlsx] [--unit mm|mil]`
- `pcb.export.model3d` — 3D 模型。`fileType` step|obj，`modelMode` Outfit|Parts，
  `element` 取自 `Component Model|Via|Silkscreen|Wire In Signal Layer`，`autoGenerateModels`。
  上游限制：只有以 STEP 导入的元件模型会出现在 STEP 导出里。→ `easyeda pcb export-3d`

平台可能**不抛错而直接返回 `undefined`**（空板 / 无板框 / 无元件 / 该构建未实现）——
这时得到点名该 API 的 `EDA_CALL_FAILED`，不会有空文件冒充成功。活动文档确凿不是 PCB 时
以 `PRECONDITION_REFUSED` 零写入拒绝；文档身份读不出来则放行（读不出不等于不是 PCB）。
能力边界与实测证据见 [pcb.md](pcb.md)。

### 长度约束：差分对 / 等长网络组（#176）

**布线前（P7 之前）声明,布线后用 `pcb report` 量。** 约束是让 DRC 与布线器知道「这两条是一对 /
这组必须等长」的唯一途径,也是 `pcb report` 的 `skew`(|lenP−lenN|)与 `spread`(max−min)有意义的前提 ——
不建约束,那两个数组永远是空的,报告里的测量能力等于空转。

- `pcb.constraint.list` — 读回本板的**约束清单**(差分对 + 等长组)。注意与 `pcb.report` 分工:
  这条给「有哪些约束」,`pcb.report` 给「量出来多少」→ `easyeda pcb diff-pair list` / `eq-group list`
- `pcb.differential_pair.create|delete|rename` → `easyeda pcb diff-pair create --name USB0 --positive USB_DP --negative USB_DM`
- `pcb.equal_length_group.create|add_nets|delete` → `easyeda pcb eq-group create --name DDR_ADDR --nets A0,A1,A2`

四条行为约定(都已真机验过):

1. **网名前置校验**:约束指向板上没有的网,平台照收不误但等于没建 —— 我方在动手前比对
   `pcb nets`,对不上就**一个字节都不写**地拒绝并点名缺失网(网名大小写敏感,来自原理图);
2. **写后回读**:回执的 `verified` 是连接器自己重读 `getAll` 比对出来的,平台返回的 boolean 不算数;
3. **幂等**:同名同内容重建 = `alreadyExists`(可重放);同名**不同**内容 = 明确拒绝并给下一步
   (改名 / 先删 / 用 `eq-group add` 扩展),绝不静默覆盖;
4. **改绑定要删了重建**:平台对差分对只暴露「改名」,没有「改绑哪两条网」。

这些是 `Mutates` 动作。即时读取可能带 `staleRisk`，可用于诊断；最终约束证据使用
`pcb save → doc reload → list/report`。

## Board（板子/组合 — 原理图↔PCB 绑定）

一个 **Board = 1 张原理图 + 1 块 PCB**，原理图与 PCB 就是通过它「组合」在一起（`import_changes` 也沿此链接同步）。Board 以**名称**标识。CLI：`easyeda board …`。

- `board.list` / `board.current` — 列出全部组合（名称 + 原理图 + PCB）/ 当前组合
- `board.create` — 把原理图和/或 PCB 绑成新组合（`--schematic` / `--pcb`）；游离 PCB 在 `import_changes` 前的修复手段
- `board.rename` — 重命名组合（`--name` → `--new`）
- `board.copy` — 复制组合（连同原理图 + PCB）
- `board.delete` — 删除组合（**需确认**，无 undo）


## PCB 属性同步（现有契约）

- `pcb.component.attrs_backfill` — **PCB 器件属性回填（器件标准化 PCB 侧）**。平台 sch→PCB 导入把 otherProperty 建成**键在值空**（Value/耐压/精度/Datasheet 全 ""），且原理图实例属性值 save/reload 后同样为空（不可作源）——唯一稳定源是 **device 库记录**：按实例 C 号 `getByLcscIds` 解析，只填 PCB 侧空值键（手改值优先，`--overwrite` 强制），全程 PCB 前台。无 C 号器件跳过并报告。`pcb import-changes` 成功后**自动跑**（`--no-sync-attrs` 关）。⚠️ **平台投影键绝不参与 merge**（`Designator`/`Unique ID`/`Name`/`Add into BOM`/`Manufacturer*`/`Supplier*`——它们存在顶层图元状态；库记录的 `Designator:"C?"` 占位键灌进实例会被平台同步成图元位号,一板位号全灭 = 166/166 U? 事故真因,2026-08-09 根治）。CLI：`easyeda pcb sync-attrs [--overwrite]`
- `pcb sync-designators`（`pcb.components.list` + `pcb.component.modify` 编排,无新 action）— **修占位位号**（`U?`/`C?`）：按 `uniqueId`（平台首次导入铸造、跨文档同一命名空间）从原理图回填。只动占位符（手设真实位号绝不覆盖）；每笔回读验证；修完立落 `pcb.save` 检查点；原理图侧同为占位符的件归类「先标注原理图」。`--dry-run`/`--json`（Failed>0 非零退出）。`import-changes` 后自动**殿后**跑（在 attrs 之后,`--no-sync-designators` 关）。CLI：`easyeda pcb sync-designators`

3D 模型导入的 `/action` 请求体上限为 **32 MiB**（#199），计算的是包含 base64、
文件名和其他字段的整个 JSON，不是原始模型大小。base64 约膨胀 4/3，
因此原始模型必须小于约 24 MiB，并给 JSON 字段留出余量。超过上限会在 daemon
入口拒绝，不会交给连接器；请压缩/简化模型或使用库中已有模型。
### `schematic.pin.repair_marker`

受保护的单脚标记支路替换。输入含页面身份、稳定组件/脚、旧 wire+marker 的完整坐标/ID、
目标 kind/net/direction/offset 和源快照哈希。daemon 在同一互斥区间内读取基线、验证旧对象，
删除旧支路、创建新支路并回读；目标 finding 必须消失，范围外对象与旧 finding 必须不变。
部分写入如实返回，不能重试或声称回滚。只由 `sch layout-edit --playbook` 生成；普通修线不手写。
