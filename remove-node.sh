#!/usr/bin/env bash
# ============================================================================
# TempMail — One-Click Remove Node
# ============================================================================
# Run this script ON THE NODE YOU WANT TO REMOVE.
# It safely stops the mail-edge container + cleans up Docker data.
#
# IMPORTANT: Remove DNS records FIRST, then wait 1 hour, then run this script.
#
# Usage:
#   chmod +x remove-node.sh
#   ./remove-node.sh
# ============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
echo "╔══════════════════════════════════════════════════════════╗"
echo "║       TempMail — Remove Mail-Edge Node                   ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo -e "${NC}"

# ---------------------------------------------------------------------------
# Pre-flight check
# ---------------------------------------------------------------------------
echo -e "${YELLOW}=== Pre-flight Checks ===${NC}"
echo ""

# Check if docker-compose.node.yml exists
if [ ! -f "docker-compose.node.yml" ]; then
  echo -e "${RED}✗ docker-compose.node.yml not found.${NC}"
  echo "  This script must be run from the mailserver directory on a secondary node."
  echo "  If this is the PRIMARY server, do NOT remove it — it runs the entire system."
  exit 1
fi

echo -e "${GREEN}✓${NC} docker-compose.node.yml found"

# Check if containers are running
if docker compose -f docker-compose.node.yml ps --quiet 2>/dev/null | grep -q .; then
  echo -e "${GREEN}✓${NC} mail-edge container is running"
else
  echo -e "${YELLOW}!${NC} mail-edge container is already stopped"
fi

echo ""

# ---------------------------------------------------------------------------
# DNS reminder
# ---------------------------------------------------------------------------
echo -e "${RED}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${RED}║  ⚠️  Have you ALREADY removed DNS records?              ║${NC}"
echo -e "${RED}║                                                          ║${NC}"
echo -e "${RED}║  1. Delete MX record pointing to this node              ║${NC}"
echo -e "${RED}║  2. Delete A record for this node's hostname            ║${NC}"
echo -e "${RED}║  3. Wait at least 1 hour for DNS propagation            ║${NC}"
echo -e "${RED}╚══════════════════════════════════════════════════════════╝${NC}"
echo ""
read -rp "Have you removed DNS records and waited? (yes/no): " DNS_CONFIRMED

if [ "$DNS_CONFIRMED" != "yes" ]; then
  echo ""
  echo -e "${YELLOW}Please remove DNS records first, wait 1 hour, then re-run this script.${NC}"
  echo ""
  echo "  Steps:"
  echo "    1. Go to your DNS provider"
  echo "    2. Delete the MX record for this node (e.g., mail2.example.com)"
  echo "    3. Delete the A record for this node's hostname"
  echo "    4. Wait 1 hour for worldwide DNS propagation"
  echo "    5. Re-run: ./remove-node.sh"
  echo ""
  exit 0
fi

# ---------------------------------------------------------------------------
# Stop containers
# ---------------------------------------------------------------------------
echo ""
echo -e "${YELLOW}[1/3] Stopping mail-edge container...${NC}"
docker compose -f docker-compose.node.yml down 2>/dev/null || true
echo -e "  ${GREEN}✓${NC} Container stopped"

# ---------------------------------------------------------------------------
# Clean up Docker data
# ---------------------------------------------------------------------------
echo ""
echo -e "${YELLOW}[2/3] Cleaning up Docker data...${NC}"
docker compose -f docker-compose.node.yml down --volumes --rmi all 2>/dev/null || true
docker system prune -f 2>/dev/null || true
echo -e "  ${GREEN}✓${NC} Docker data cleaned"

# ---------------------------------------------------------------------------
# Print primary server cleanup instructions
# ---------------------------------------------------------------------------
NODE_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "THIS_NODE_IP")

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║           ✅ NODE REMOVED SUCCESSFULLY                  ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${YELLOW}=== Run these commands on the PRIMARY server ===${NC}"
echo ""
echo -e "  ${CYAN}# Remove firewall rules for this node${NC}"
echo "  sudo ufw delete allow from ${NODE_IP} to any port 6379"
echo "  sudo ufw delete allow from ${NODE_IP} to any port 4000"
echo "  sudo ufw delete allow from ${NODE_IP} to any port 11333"
echo ""
echo -e "  ${CYAN}# If using WireGuard: remove this node's [Peer] block${NC}"
echo "  sudo nano /etc/wireguard/wg0.conf"
echo "  sudo wg-quick down wg0 && sudo wg-quick up wg0"
echo ""
echo -e "${YELLOW}=== Verification (on primary server) ===${NC}"
echo ""
echo "  curl https://YOUR_API_URL/health        # → {\"status\":\"ok\"}"
echo "  dig +short example.com MX               # → no removed node"
echo "  sudo ufw status                         # → no old rules"
echo ""
echo -e "${GREEN}Mail will automatically route to remaining nodes. No changes needed on your web app.${NC}"
echo ""
