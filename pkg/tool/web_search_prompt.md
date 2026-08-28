## Web Search

Use web_search to find current documentation, API references, solutions, or information beyond your training cutoff.
After searching, use web_fetch to read the full content of promising URLs.
For known URLs, skip web_search and use web_fetch directly.

Execution mode is determined by how web_search appears in your tool list:
- If web_search is a callable function (with a schema): call it directly. It runs a local backend — DuckDuckGo (default, no configuration needed) or Brave Search (set BRAVE_API_KEY environment variable for better results).
- If web_search is a server-side tool (no callable function schema): search executes automatically server-side when the provider decides it is needed, and results are injected into the conversation. Do not attempt to call it — use the injected results directly.
