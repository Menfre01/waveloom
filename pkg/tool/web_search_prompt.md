## Web Search

Use web_search to find current documentation, API references, solutions, or information beyond your training cutoff.
After searching, use web_fetch to read the full content of promising URLs.
For known URLs, skip web_search and use web_fetch directly.

Backends (auto-selected, first available wins):
- DeepSeek Search (in-house engine, used when the LLM provider is deepseek with an API key).
  Results carry credibility signals: `score` (relevance 0-3, >=1.5 is high quality),
  `authority` (0-4, >=3 is authoritative), and a publication date when known.
  A `Summary:` line is attached only to the top high-quality results — fetch the URL
  for the full text when you need more than the snippet.
- Brave Search (set BRAVE_API_KEY for better results than DuckDuckGo)
- DuckDuckGo (default fallback, no configuration needed)

Reading the output:
- No score/authority line means a local backend served the result: ranking is
  unverified, so prefer primary sources and say so when evidence is thin.
- A `[fallback]` line explains why the preferred backend was not used. It is
  informational, not an error — retry later if you need the richer signals.
