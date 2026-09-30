# 泄洪闸门操作协调服务

## 项目目标

该服务用于水工控制室协调泄洪闸门的命令申请、安全联锁复核和单次开度调整。系统在同一闸门上只允许一条已通过复核且尚未结束的执行命令，任何冲突都必须被显式拒绝，不能覆盖已经占用的闸门。

## 使用角色

- 运行协调员：登记闸门并提交开度调整命令。
- 安全复核员：提交联锁结论，决定命令是否通过。
- 闸门操作员：凭执行令牌启动命令并登记执行结果。

## 核心实体

- `gate`：闸门及其当前开度、运行状态、最大开度和活动命令。
- `gate_command`：一次开度调整申请，包含目标开度、申请原因、状态和执行令牌。
- `interlock_decision`：安全复核结论，状态为 clear 或 blocked。

## 业务流程

### register-gate

运行协调员通过 `POST /gates` 登记闸门。服务校验标识、名称、最大开度和初始开度，写入空闲状态；重复标识返回稳定冲突错误，不覆盖原记录。可同时配置一个值班周期的滚动开度调整窗口（时长秒数与周期内累计调整上限），两项必须同时提供或同时缺省；缺省时不启用窗口限制。

### request-gate-command

运行协调员通过 `POST /commands` 提交开度调整命令。服务读取闸门当前开度和限制，校验目标开度与申请信息，创建待复核命令并冻结起始开度。若闸门启用了调整窗口，命令的调整量（目标开度与冻结起始开度之差的绝对值）叠加窗口内已生效执行事实的累计调整量后超过安全上限，则返回稳定冲突 `adjustment_window_exceeded`，命令不会写入。

### review-safety-interlock

安全复核员通过 `POST /commands/{id}/interlock` 提交复核结论。clear 结论会原子占用闸门并签发一次执行令牌；blocked 结论会把命令置为 rejected。其他命令先占用同一闸门时，当前 clear 结论返回冲突且不改变闸门。对启用窗口的闸门，clear 结论在同一临界区内按已完成命令重新计算窗口：累计调整量会超限时，在占用落库和执行令牌签发之前以 `adjustment_window_exceeded` 拒绝，命令保持 pending、版本不变、闸门不被占用。

### execute-gate-command

闸门操作员通过 `POST /commands/{id}/execute` 提交执行令牌。服务校验命令、令牌、闸门占用关系和目标开度，在临界区内完成 approved、executing、completed 或 aborted 的转换，并同步更新闸门状态。令牌在命令结束后失效，不能复用。

## 状态与规则

- `gate` 状态：idle、reserved、moving、faulted。
- `gate_command` 状态：pending、approved、rejected、executing、completed、aborted。
- 只有 pending 命令可以复核。
- 只有 approved 命令可以执行，且执行令牌必须匹配。
- clear 复核与闸门占用必须同时成功；任一步失败时两者都不写入。
- blocked 复核使命令 rejected，不会占用闸门。
- 模拟故障会令命令 aborted、闸门 faulted，并保留最终开度。
- 已完成或已中止的命令不能再次执行。

## 开度调整窗口

- 窗口按闸门配置：`adjustment_window_seconds` 为滚动值班周期时长，`adjustment_window_max_travel` 为周期内累计调整量安全上限；两者必须成对配置，缺省表示不启用。
- 窗口用量只统计**已生效执行事实**：在窗口区间内（含起点）进入 `completed` 的命令，按其冻结起始开度与目标开度之差的绝对值累计；pending、approved、rejected、aborted 命令都不占用额度。
- 合法边界为闭区间：已用调整量与本次调整量之和小于等于上限即放行，正好等于上限也合法。
- 申请阶段与 clear 复核阶段都在仓库互斥锁内重新计算窗口；超限时返回稳定冲突码 `adjustment_window_exceeded`，被拒请求不落命令、不占用闸门、不签发执行令牌、不推进闸门或命令版本。
- 执行令牌在所有复核检查（状态、占用、窗口）通过后才在临界区内签发，因此被拒路径不会产生任何凭据。
- 已完成命令滚出窗口后真实释放额度，但命令历史仍可通过 `GET /commands/{id}` 查询；`completed_at` 标记其生效时刻。
- `GET /gates/{id}/adjustment-window` 返回窗口是否启用、周期时长、上限、窗口起点、评估时刻、已用与剩余调整量以及构成用量的事实清单，保证合法边界、真实释放与历史可解释。

## 模块边界

- `cmd/server`：进程入口、监听地址和退出控制。
- `internal/httpapi`：HTTP 路由、JSON 解码、状态码映射。
- `internal/coordination`：工作流编排、命令标识和执行令牌签发。
- `internal/domain`：实体、不变量和状态转换规则。
- `internal/storage`：带互斥保护的内存仓库和原子写入。
- `internal/contracts`：API 请求、响应和稳定错误结构。
- `internal/bootstrap`：运行时依赖装配。

## 接口

- `GET /health`
- `POST /gates`
- `GET /gates/{id}`
- `GET /gates/{id}/adjustment-window`
- `POST /commands`
- `GET /commands/{id}`
- `POST /commands/{id}/interlock`
- `POST /commands/{id}/execute`

错误响应统一为：

```json
{"code":"stable_error_code","message":"可读说明"}
```

## 验证计划

本初始化基线刻意不生成单元测试、夹具或浏览器测试文件，也不声明 `test_command`。后续任务阶段负责补充测试，并在该阶段建立红绿验证。当前每条工作流通过 `cmd/workflowcheck` 启动真实 HTTP 服务并调用公开接口，校验成功状态、持久化结果和关键失败分支。
