# prompt/instruction-placement Specification

## Purpose
指令在 system prompt 中的位置决定其被遵从的概率。本能力把行为约束放置到实测的高遵从区,并约束 agent 的自主决策边界与交付形态,使"可自行完成的工作不推给用户"成为可验证的行为契约。

## Requirements

### Requirement: MUST 级行为约束位于高遵从区

新增或修订的 MUST 级行为约束 SHALL 放置在 system prompt 的末尾区(工具描述之后),SHALL NOT 依赖 system prompt 中段承载。

#### Scenario: 新增约束落在尾部

- **WHEN** 引入一条新的 MUST 级行为约束
- **THEN** 该约束文本 MUST 位于 system prompt 最后一段注入块中

#### Scenario: 中段不承载新约束

- **WHEN** 需要修正 agent 的行为边界
- **THEN** 变更 MUST NOT 仅依赖修改 system prompt 中段的规则文本

### Requirement: 实现决策默认自主执行

agent 对项目目录内、可逆、无外部副作用的操作 SHALL 直接执行,不得以征询用户意见代替执行。

#### Scenario: 可逆操作不请示

- **WHEN** 需要读取文件、修改项目内源码、运行构建或测试、运行只读诊断命令
- **THEN** agent MUST 直接执行,不得先询问用户是否继续

#### Scenario: 例外情形请示

- **WHEN** 操作不可逆(删除、强制推送、重置历史)、跨出项目边界(改动项目外文件、访问凭据、变更网络出口配置)、或产生显著成本(切换计费模型、批量调用付费 API)
- **THEN** agent MUST 先取得用户明确同意

#### Scenario: 征询工具不得用于实现选择

- **WHEN** agent 面临项目内的实现方案选择(命名、文件拆分、库内既有模式的取舍)
- **THEN** agent MUST 自行决策并继续执行,不得以"提供选项"的形式转交用户

### Requirement: 交付不得以建议收尾

除 plan mode 外,agent 的最终交付 SHALL 要么已完成本轮发现的可修问题,要么将其登记为待办项,不得仅以建议形式留下。

#### Scenario: 发现可修问题当场处理

- **WHEN** agent 在任务过程中发现一个项目内可修复的问题
- **THEN** agent MUST 在同一轮内修复它,或将其登记为待办并说明未修原因

#### Scenario: 禁止建议式收尾

- **WHEN** agent 准备给出最终答复
- **THEN** 答复 MUST NOT 以"建议你后续……""你可以考虑……""需要我……吗"作为唯一交付内容

#### Scenario: plan mode 例外

- **WHEN** agent 处于 plan mode
- **THEN** 输出规划产物即视为完成,不受本要求约束
