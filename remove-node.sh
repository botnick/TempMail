#!/usr/bin/env bash
# ============================================================================
# TempMail Platform — Remove Mail-Edge Node
# ============================================================================
# Run this ON THE NODE you want to decommission.
#
# IMPORTANT: Remove DNS records FIRST, wait ~1 hour, then run this script.
#
# Usage:
#   chmod +x remove-node.sh && ./remove-node.sh
#   ./remove-node.sh --force    # Skip all confirmations
# ============================================================================

set -euo pipefail

# Source shared library
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

# --- Parse flags ---
FORCE_MODE=false
for arg in "$@"; do
    case "$arg" in
        --help|-h)    show_help "remove-node.sh" "Safely remove a secondary mail-edge node from the cluster." ;;
        --version|-v) show_version ;;
        --force|-f)   FORCE_MODE=true ;;
    esac
done

# --- Pre-flight checks ---
preflight_checks() {
    log_step "Pre-flight Checks"

    # Ensure we are on a secondary node
    if [ ! -f "docker-compose.node.yml" ]; then
        log_error "docker-compose.node.yml not found.\n  This script must be run from the mailserver directory on a SECONDARY node.\n  If this is the primary server, do NOT remove it — it runs the entire system."
    fi
    log_success "docker-compose.node.yml found."

    # Check container status
    if $SUDO docker compose -f docker-compose.node.yml ps --quiet 2>/dev/null | grep -q .; then
        log_success "mail-edge container is currently running."
    else
        log_warn "mail-edge container is already stopped."
    fi
}

# --- DNS warning and confirmation ---
confirm_dns_removal() {
    log_step "DNS Verification"

    echo -e "  ${RED}${BOLD}╔══════════════════════════════════════════════════════╗${NC}"
    echo -e "  ${RED}${BOLD}║  ⚠  BEFORE REMOVING THIS NODE:                     ║${NC}"
    echo -e "  ${RED}${BOLD}║                                                      ║${NC}"
    echo -e "  ${RED}${BOLD}║  1. Delete MX record pointing to this node          ║${NC}"
    echo -e "  ${RED}${BOLD}║  2. Delete A record for this node's hostname        ║${NC}"
    echo -e "  ${RED}${BOLD}║  3. Wait at least 1 hour for DNS propagation        ║${NC}"
    echo -e "  ${RED}${BOLD}╚══════════════════════════════════════════════════════╝${NC}"

    if [[ "$FORCE_MODE" == true ]]; then
        log_warn "--force flag used. Skipping DNS confirmation."
        return
    fi

    echo ""
    read -rp "$(echo -e "${CYAN}? Have you removed DNS records and waited? (yes/no): ${NC}")" DNS_CONFIRMED

    if [[ "$DNS_CONFIRMED" != "yes" ]]; then
        echo ""
        log_info "Please perform these steps first:"
        echo -e "  ${DIM}1. Go to your DNS provider${NC}"
        echo -e "  ${DIM}2. Delete the MX record for this node${NC}"
        echo -e "  ${DIM}3. Delete the A record for this node's hostname${NC}"
        echo -e "  ${DIM}4. Wait 1 hour for worldwide DNS propagation${NC}"
        echo -e "  ${DIM}5. Re-run: ./remove-node.sh${NC}"
        echo ""
        exit 0
    fi
}

# --- Final confirmation ---
confirm_removal() {
    if [[ "$FORCE_MODE" == true ]]; then
        return
    fi

    echo ""
    echo -e "  ${YELLOW}This will permanently stop and remove the mail-edge container"
    echo -e "  and all associated Docker data on this node.${NC}"
    echo ""
    if ! confirm_action "Proceed with removal?" "n"; then
        log_info "Aborted."
        exit 0
    fi
}

# --- Stop containers ---
stop_containers() {
    log_step "Stopping Containers"

    log_info "Stopping mail-edge..."
    $SUDO docker compose -f docker-compose.node.yml down 2>/dev/null || true
    log_success "Container stopped."
}

# --- Clean up Docker data ---
cleanup_docker() {
    log_step "Cleaning Up"

    log_info "Removing containers, volumes, and images..."
    $SUDO docker compose -f docker-compose.node.yml down --volumes --rmi all 2>/dev/null || true

    log_info "Pruning unused Docker data..."
    $SUDO docker system prune -f 2>/dev/null || true

    log_success "Docker data cleaned."
}

# --- Summary ---
print_summary() {
    local NODE_IP
    NODE_IP=$(get_public_ip)

    echo -e "\n${GREEN}${BOLD}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}${BOLD}║          ✅  NODE REMOVED SUCCESSFULLY                   ║${NC}"
    echo -e "${GREEN}${BOLD}╚══════════════════════════════════════════════════════════╝${NC}"

    echo -e "\n${YELLOW}${BOLD}── Run on PRIMARY Server ──${NC}"
    echo -e "  ${DIM}Remove firewall rules for this node (${NODE_IP}):${NC}"
    echo -e "  sudo ufw delete allow from ${NODE_IP} to any port 6379"
    echo -e "  sudo ufw delete allow from ${NODE_IP} to any port 4000"
    echo -e "  sudo ufw delete allow from ${NODE_IP} to any port 11333"

    echo -e "\n  ${DIM}If using WireGuard, remove the [Peer] block:${NC}"
    echo -e "  sudo nano /etc/wireguard/wg0.conf"
    echo -e "  sudo wg-quick down wg0 && sudo wg-quick up wg0"

    echo -e "\n${CYAN}${BOLD}── Verification (on primary) ──${NC}"
    echo -e "  curl https://YOUR_API_URL/health     ${DIM}# → {\"status\":\"ok\"}${NC}"
    echo -e "  dig +short yourdomain.com MX          ${DIM}# → no removed node${NC}"
    echo -e "  sudo ufw status                       ${DIM}# → no old rules${NC}"

    echo -e "\n${GREEN}  Mail will automatically route to remaining nodes.${NC}"
    echo -e "${GREEN}  No changes needed on your web app.${NC}"

    print_elapsed
}

# --- Main ---
main() {
    print_banner "Remove Mail-Edge Node"
    check_root
    preflight_checks
    confirm_dns_removal
    confirm_removal
    stop_containers
    cleanup_docker
    print_summary
}

main
