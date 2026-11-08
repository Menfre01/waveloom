## Purpose

system prompt 是每轮请求的第一条消息,其文本完整性决定指令能否被模型正确解析。本能力约束 system prompt 源文件不得含源码拼接残留、引用的 Markdown 节必须真实存在、分节结构合法、数字断言与实际定价一致。

## ADDED Requirements

### Requirement: system prompt 文本不得含源码拼接残留

system prompt 源文本(经 `go:embed` 原样进入请求)SHALL NOT 包含任何源代码字符串拼接语法残留,例如字面的反引号-加号-引号序列。

#### Scenario: 源文件不含拼接片段

- **WHEN** 读取 system prompt 源文件全文
- **THEN** 全文 MUST NOT 出现 `` ` + " `` 或 `` " + ` `` 形态的拼接片段

#### Scenario: 发布前阻断

- **WHEN** 构建或发布流程运行提示词完整性测试
- **THEN** 检测到拼接残留时测试 MUST 失败并指出文件名与行号

### Requirement: 被引用的 Markdown 节必须存在

任何工具描述或提示词片段引用的 system prompt 章节标题 SHALL 在 system prompt 中真实存在。

#### Scenario: 引用的节存在

- **WHEN** 某个工具的描述引用了形如 `## <标题>` 的章节
- **THEN** system prompt 全文 MUST 包含该标题

#### Scenario: 悬空引用被检出

- **WHEN** 工具描述引用的章节在 system prompt 中不存在
- **THEN** 完整性测试 MUST 失败并列出该引用与缺失的标题

### Requirement: 分节结构合法

system prompt 的 Markdown 标题 SHALL 与其上一段内容之间以空行分隔,保证标题被解析为节而非段落文本。

#### Scenario: 标题前有空行

- **WHEN** system prompt 中出现 `##` 或 `###` 级标题
- **THEN** 该行之前 MUST 为空行或文档起始

### Requirement: 成本断言与实际定价一致

system prompt 中涉及 token 成本倍数的表述 SHALL 与项目定价表的实际数值一致,并 MUST 明确比较对象(缓存命中输入、未命中输入或输出)。

#### Scenario: 倍数按定价表可复核

- **WHEN** system prompt 声称某类 token 相对另一类为 N 倍成本
- **THEN** 该 N 值 MUST 能由定价表中对应两项单价之比复现,且比较对象 MUST 在正文中写明
