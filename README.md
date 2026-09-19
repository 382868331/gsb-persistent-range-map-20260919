# 持久化区间覆盖与差异查询库

离线编辑器的区间样式库：int64 半开区间 `[lo,hi)` 到字符串的**不可变**映射。
每次编辑返回新版本，旧版本保持可读；`Diff` 给出两个版本间需要重绘的范围。

仅依赖 Go 1.26.5 标准库，无第三方依赖，Windows 原生离线可运行。

## 运行

```sh
go run ./cmd/demo                    # 演示：正常结果 + 一个实际触发的失败，约 2 秒
go test ./... -count=1 -timeout=60s  # 全部测试
```

## 接口（包 `rangemap`）

| 签名 | 说明 |
|---|---|
| `New() *Map` | 空版本 |
| `(*Map).Set(lo, hi int64, val string) (*Map, Stats, error)` | 覆盖 `[lo,hi)`，返回新版本；同值相邻段合并，边界处切分已有段 |
| `(*Map).Erase(lo, hi int64) (*Map, Stats, error)` | 撤销 `[lo,hi)` 的覆盖，切分跨边段 |
| `(*Map).At(x int64) (string, bool)` | 点查询，显式区分覆盖/未覆盖 |
| `(*Map).Range(lo, hi int64) ([]Fragment, Stats, error)` | 窗口内覆盖片段，裁剪、按坐标升序、同值相邻合并 |
| `(*Map).Fragments() []Fragment` | 整版本全部覆盖段 |
| `(*Map).Len() int` / `(*Map).Equal(o *Map) bool` | 片段数 / 语义相等 |
| `Diff(old, new *Map, lo, hi int64) ([]DiffRange, Stats, error)` | 窗口内差异 run 列表 |
| `ApplyDiff(old *Map, diffs []DiffRange) (*Map, Stats, error)` | 用差异重建窗口内的新状态 |

`Stats{Created, Visited, Skipped int}`：本次操作实际**新建节点数 / 访问节点数 /
共享跳过节点数**。

## 语义要点

- 所有区间参数必须 `lo < hi`，否则返回 `ErrInvalidRange`，**原版本不改变**
  （直接返回接收者，统计为零）。
- 空字符串 `""` 是合法值；它与“未覆盖”不同：`At` 的 `bool` 区分二者，
  `Range`/`Fragments` 只返回被覆盖的片段（包括空值段），空洞即缺失。
- 端点允许 `MinInt64` 与 `MaxInt64`；点 `MaxInt64` 永不被覆盖；实现中
  **不做 `hi+1` 转换**，无溢出。
- 相邻且同值的段自动合并（左侧、右侧、整体覆盖都覆盖）。
- 单版本片段上限 10000（`MaxFragments`），超限返回 `ErrTooManyFragments`，
  旧版本不变。

## Diff 与重绘

`DiffRange{Lo,Hi, OldCovered,Old, NewCovered,New}` 是窗口内“旧状态/值或新状态/值”
保持恒定的极大差异段；只有相邻段两侧状态与值分别相同才合并。因此：

- 旧值同为 `a`、新值分别为 `b`/`c` 的两段相邻差异**不会**合并；
- 旧值不同、新值相同同理不合并；
- 相同旧/新对覆盖多个坐标时合并为一个 run。

持久化的结构共享让“相同”可直接用指针判定：双路中序遍历时遇到两侧同一节点且
其包围盒完全落在窗口内，则该子树必然逐点相同，整棵跳过并计入 `Skipped`。

对 `Diff(old,new,lo,hi)` 的结果调用 `ApplyDiff(old, …)`（新覆盖→`Set`，
变空洞→`Erase`）即可在窗口内重建出 `new`，窗口外与 `old` 一致。

## 实现与复杂度

`rangemap/tree.go`：持久化 AVL，节点存 `lo,hi,val` 与 `height,size,minLo,maxHi`。
任何修改都沿根到叶**复制路径**，未触及子树与旧版本共享；旋转也是非破坏式的。
包围盒使范围收集可以整体剪枝无关子树。

- 点查询：`O(log(n+1))`
- `Set`/`Erase`：与 `k` 个相交段交互，`O((k+1) log(n+1))` 次新建/访问
- `Diff`：线性于窗口内输出与访问量，输出 `O(k)`，可整体线性输出
- 范围查询结果线性于返回片段数

测试在 `[-16,16)` 整数宇宙上用固定种子（220 次随机编辑 + 跨版本 Diff）与
逐点暴力参考模型比对 `At`/`Range`/`Fragments`，并校验历史版本、Diff 精确性与
合并规则、Diff 重建议、AVL 结构（平衡/BST 序/包围盒）；另含覆盖切分、
空值/空洞、相邻合并、不同旧新值对的相邻差异、int64 端点、旧版本不变、
片段上限和稀疏局部改动的节点计数等用例。随机用例固定种子、小样本，无压力测试。

## 目录

```
rangemap/          库实现（tree.go / map.go / diff.go）与测试
cmd/demo/main.go   演示
TASK.md            完整任务规格
```
