#!/usr/bin/env bash
# Wire this workstation to the ubiqo fabric in one command.
#   attach.sh <ubq_token> [project]
set -euo pipefail

TOKEN="${1:?usage: attach.sh <ubq_token> [project]   (tokens: docker compose logs seed)}"
PROJECT="${2:-website-redesign}"
SERVER="${UBIQO_SERVER_URL:-http://ubiqo:8383}"

echo "── ubiqo login"
ubiqo login --server "$SERVER" --token "$TOKEN"

echo "── bind project directory"
mkdir -p "/root/work/$PROJECT"
cd "/root/work/$PROJECT"
ubiqo init --project "$PROJECT"

echo "── register the MCP connector with claude-code"
claude mcp remove ubiqo --scope user >/dev/null 2>&1 || true
claude mcp add --transport http --scope user ubiqo "$SERVER/mcp" \
  --header "Authorization: Bearer $TOKEN"

echo "── install session hooks (context injection + activity reporting)"
mkdir -p /root/.claude
cat > /root/.claude/settings.json <<'JSON'
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "ubiqo hook session-start", "timeout": 10 } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "ubiqo hook session-end", "timeout": 10 } ] }
    ]
  }
}
JSON

echo
echo "attached to $SERVER as: $(ubiqo context pull --project "$PROJECT" 2>/dev/null | sed -n 's/^You are ubiqo user \`\([a-z0-9-]*\)\`.*/\1/p' | head -1)"
echo
echo "next:  claude        (sign in — use a DIFFERENT claude.ai account per workstation)"
echo "then ask:  \"What happened in $PROJECT while I was away?\""
