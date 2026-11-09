# tool/web-fetch-backend Specification

## Purpose
抓取工具的后端选择与降级契约。目标是在保留现有 URL 安全校验与内容体积限制的前提下,用官方抓取端点获得更干净的 Markdown 内容,并在其不可用时无感降级。

## Requirements

### Requirement: 抓取后端选择优先级

抓取 SHALL 按"官方抓取端点 → 本地抓取"的顺序选择,官方端点不可用时自动降级。

#### Scenario: 凭据可用时走官方端点

- **WHEN** 当前 provider 提供官方抓取所需的 API key 与 base URL
- **THEN** 抓取 MUST 优先请求官方端点

#### Scenario: 官方端点不可用时降级

- **WHEN** 官方端点返回内容不可用、安全策略拒绝、限流或超时
- **THEN** 抓取 MUST 降级到本地抓取并返回其结果

#### Scenario: 官方无内容时允许降级

- **WHEN** 官方端点表示无法从目标页面提取可用内容
- **THEN** 抓取 MUST 尝试本地抓取,不得直接判定该 URL 不可访问

### Requirement: URL 安全校验不因后端切换而放松

无论使用哪个后端,抓取 MUST 在发起请求前完成与现有实现等效的 URL 校验,包括协议限制与内网地址拒绝。

#### Scenario: 内网地址在两个后端都被拒绝

- **WHEN** 请求的 URL 指向内网或回环地址
- **THEN** 两个后端 MUST 都在发出请求前拒绝该 URL

#### Scenario: 非 HTTP(S) 协议被拒绝

- **WHEN** 请求的 URL 使用非 HTTP(S) 协议
- **THEN** 抓取 MUST 拒绝该请求,不得因后端切换而放行

### Requirement: 内容体积与类型限制保持

官方后端返回的内容 SHALL 遵守与本地后端一致的体积上限与文本化要求。

#### Scenario: 超限内容被截断并告知

- **WHEN** 抓取结果超出体积上限
- **THEN** 输出 MUST 截断并说明已截断,不得静默丢弃
