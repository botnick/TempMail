#!/usr/bin/env bash
# ============================================================================
# TempMail — One-Click Add Node (Additional mail-edge)
# ============================================================================
# Run this script on a NEW VPS to add a secondary mail-edge node.
# It only runs the mail-edge container, connecting to your existing infra.
#
# Usage:
#   chmod +x add-node.sh
#   ./add-node.sh
# ============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "╔══════════════════════════════════════════════════════════╗"
echo "║       TempMail — Add Mail-Edge Node                     ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo -e "${NC}"

echo -e "${YELLOW}Enter the connection details from your primary server:${NC}"
echo ""

read -rp "Redis URL (redis://:password@primary-ip:6379): " REDIS_URL
read -rp "Internal API URL (http://primary-ip:4000/internal/mail/ingest): " INTERNAL_API_URL
read -rsp "Internal API Token: " INTERNAL_API_TOKEN
echo ""
read -rp "Rspamd URL (http://primary-ip:11333) [or leave empty to skip spam check]: " RSPAMD_URL

RSPAMD_URL=${RSPAMD_URL:-}

echo ""
echo -e "${YELLOW}Building mail-edge container...${NC}"

# Create minimal docker-compose for this node
cat > docker-compose.node.yml << EOF
version: '3.8'

services:
  mail-edge:
    build:
      context: .
      dockerfile: docker/Dockerfile.mail-edge
    restart: always
    ports:
      - "25:2525"
    environment:
      - REDIS_URL=${REDIS_URL}
      - INTERNAL_API_URL=${INTERNAL_API_URL}
      - INTERNAL_API_TOKEN=${INTERNAL_API_TOKEN}
      - RSPAMD_URL=${RSPAMD_URL}
      - SPAM_REJECT_THRESHOLD=15
      - LOG_FILE_PATH=/var/log/tempmail/mail-edge.log
      - LOG_MAX_AGE_DAYS=14
      - LOG_LEVEL=info
    volumes:
      - maillogs:/var/log/tempmail

volumes:
  maillogs:
EOF

docker compose -f docker-compose.node.yml build --no-cache
docker compose -f docker-compose.node.yml up -d

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║            ✅ NODE ADDED SUCCESSFULLY                   ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  This node is now receiving SMTP on port 25"
echo -e "  and forwarding to the primary API server."
echo ""
echo -e "${YELLOW}=== DNS CONFIGURATION ===${NC}"
echo -e "  Add an MX record for this node's IP:"
echo -e "    MX   yourdomain.com  →  THIS_SERVER_IP  (priority 20)"
echo ""
echo -e "  The primary MX should have priority 10."
echo -e "  This gives you automatic failover."
echo ""
