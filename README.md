# 持久化区间覆盖与差异查询库

离线编辑器样式区间的不可变版本库：把 int64 半开区间 `[lo, hi)` 映射到字符串；每次
编辑产生新版本，旧版本持续可读，并能求出两个版本之间需要重绘的坐标范围。

- 纯 Go 1.26.5 标准库，无第三方依赖，Windows 原生离线可运行
- 后端：持久化 AVL 有序树（以 `lo` 为键，子树增广最大 `hi`），路径复制 + 结构共享
- 空字符串 `""` 是合法覆盖值，与"未覆盖"严格区分
- 端点允许 `math.MinInt64` / `math.MaxInt64`；`MaxInt64` 点本身永不被覆盖；
  全程不做 `hi+1` 转换，无溢出
- 单版本上限 10000 个规范化片段

## 目录

```
rangemap/        库实现与测试
cmd/demo/        演示：正常结果 + 实际触发的失败 + 节点计数
```

## 接口

包 `rangemap`，主要类型与函数：

```go
func New() *Map                              // 全未覆盖版本（nil *Map 等价）

func (m *Map) Set(lo, hi int64, value string) (*Map, error)   // 覆盖
func (m *Map) Erase(lo, hi int64) (*Map, error)               // 撤销覆盖（变空洞）
func (m *Map) Get(p int64) (value string, covered bool)       // 点查询
func (m *Map) Query(lo, hi int64) ([]Segment, error)          // 覆盖片段，裁剪+相邻合并
func (m *Map) Segments() []Segment                            // 全量覆盖片段
func (m *Map) Len() int                                        // 规范化片段数
func (m *Map) RangeMapStats() Stats                            // 本次操作新建/访问节点数

func Diff(old, new *Map, lo, hi int64) ([]Change, Stats, error)
func Apply(dst *Map, changes []Change) (*Map, error)           // 用 Diff 结果重建
```

语义要点：

- 所有区间参数要求 `lo < hi`，否则返回 `ErrInvalidRange`，且**不改变接收者**
  （返回同一个版本指针）。
- 超过 10000 片段返回 `ErrTooManySegments`，旧版本不变。
- `Set` 与左右相接的同值片段合并；`Erase` 表示未覆盖，空洞两侧即使同值也保持为
  两段（空洞是真实状态）。
- `Query` 只返回覆盖片段，按查询范围裁剪，相邻同值段合并。
- `Diff` 按坐标返回最大差异段；仅当相邻差异段的旧（覆盖状态,值）与新（覆盖状态,值）
  分别相同时才合并。两个版本共享的不可变子树按节点 id 判定，整块跳过。
- 对 `old` 依次 `Apply` Diff 结果（新覆盖则 `Set`，否则 `Erase`）可重建查询范围内
  的 `new` 状态。
- `Stats{Created, Visited}`：Created 为本次实际新建节点数，Visited 为本次访问过的
  不同旧节点数（按节点 id 去重）。点查询 O(log(n+1))，局部更新 O((k+1)log(n+1))，
  k 为与编辑范围相交的片段数；不做整树复制。

## 运行

```
go run ./cmd/demo
go test ./... -count=1 -timeout=60s
```

演示约 0.1 秒完成（远小于 8 秒预算），输出四部分：

1. Set/Erase/空值/空洞/裁剪查询/历史可读/Diff 重建的一个正常结果；
2. 固定种子（seed=42）小更新序列在 `[-16,16)` 上的逐点参考校验，含历史版本与
   Diff 重建；
3. n=5000 版本上一次稀疏局部改动的实际新建/访问节点计数；
4. 由代码实际触发的失败：`lo>=hi` 非法区间与第 10001 片段超限，错误返回且旧版本不变。

## 测试覆盖

`go test` 覆盖：覆盖切分、空值与空洞区分、相邻合并、相邻差异段的合并规则（同旧异新、
异旧同新、覆盖↔空洞）、int64 极端端点、旧版本不可变与非法输入不改旧版本、查询裁剪、
10000 片段上限、固定种子 200 步随机参考模型（含全部历史版本与多范围 Diff 重建）、
结构共享（同节点指针复用）与节点计数上界、共享子树 Diff 跳过。
