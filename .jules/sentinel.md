## 2024-03-24 - CORS Misconfiguration
**Vulnerability:** Overly permissive CORS headers (`Access-Control-Allow-Origin: *`) allowed any origin to access the API endpoints and read potentially sensitive MCP configuration and index data.
**Learning:** Hardcoding `*` in the server handler makes the HTTP API vulnerable to Cross-Origin Read Blocking bypass and general unauthorized browser access.
**Prevention:** Use an environment variable to allow a configurable list of allowed origins and dynamically reflect the matching requested origin.
