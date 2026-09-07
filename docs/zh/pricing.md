# 用量费用计算

Clipal 使用整数微美元记录用量费用（`1 USD = 1,000,000 micros`）。持久化的是请求完成时算出的费用；后续更新价格目录不会对历史用量重新计价。聚合数据会保留费用来自上游响应还是内置目录，并记录聚合中包含的全部目录版本；引入归因字段之前产生的费用标记为 `legacy_unknown`。

## 费用来源优先级

每个完成的生成请求按以下顺序取第一个可用来源：

1. 上游响应直接返回的费用。
2. 根据 token 用量和内置的 `PAIArtCom/model-specs` 目录计算费用。
3. 响应没有可用的直接费用，或无法解析到有价格的模型时，不记录费用。

直接费用支持微美元字段、USD 数值字段以及 `{amount, currency}` 费用对象。非 USD 对象不会自动换汇。单请求费用超过 1,000,000 USD 时视为无效且不入账，避免异常上游响应使持久化聚合值饱和。

费用统计是代理主流程的辅助能力。用量组件格式错误、为负、缺失或内部不一致时，只跳过该请求的费用计算，不会把原本成功的上游响应变成代理失败。持久化时，无效费用归因会降级为 `legacy_unknown`，负费用则直接忽略。

## 目录解析

价格目录嵌入 Clipal 二进制，请求处理期间不会下载可变的在线价格。模型会在移除 `models/`、`openai/`、`anthropic/`、`gemini/` 等协议包装后进行精确匹配。Claude 和 Gemini 原生请求必须匹配对应目录平台；OpenAI-compatible 请求可以使用任意目录平台，Clipal 会根据 provider 的 base URL 或名称优先选择 `openrouter/deepseek/deepseek-chat` 这类 provider 专用条目，再考虑无前缀模型条目。Clipal 不按模型名前缀继承价格；例如 `gpt-5.4-pro` 使用自己的目录条目，未知的 `gpt-5.4-*` 名称保持未计价。

计算器支持标准、batch、priority、flex token 价格，缓存读取与写入，音频输入与推理输出价格，200K/272K 上下文阶梯，以及响应报告查询次数时的按次搜索费用。单一专用价格缺失时，只会回退到同一精确模型定义的对应基础价格，不会回退到其他模型。如果请求同时使用 service tier 和长上下文阶梯，但目录没有定义所需的组合价格，Clipal 会保留用量但不计算费用，避免混用不同阶梯的价格。

Anthropic 通过 `usage.speed=fast` 报告 Claude Opus 4.6 fast 模式，而不是使用单独定价的模型 ID。对于 `claude-opus-4-6` 和 `claude-opus-4-6-20260205`，Clipal 会将目录中的全部 token 费率（包括缓存费率）乘以 6；按次搜索费用不乘以该倍率。由于目录只提供模型基础费率，这项协议专用倍率由 Claude 响应适配代码维护。

`GET /api/status` 的 `pricing_catalog` 会返回当前目录版本和来源提交。

## 更新内置目录

维护者可以使用以下命令校验并引入最新上游目录：

```bash
MODEL_SPECS_REF=<已审查的40位提交SHA> make update-model-specs
```

必须显式提供提交 SHA，以确保引入的来源不可变。脚本只读取该提交中的 `dist/catalog.json`、`dist/catalog.sha` 和 `LICENSE`，不会执行上游代码；它会校验必需字段，根据 `models` 和 `clients` 重新计算版本，并同步更新内置目录、完整文件校验值、许可证和来源提交。
