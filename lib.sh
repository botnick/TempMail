#!/usr/bin/env bash
# ============================================================================
# TempMail Platform — Shared Library
# ============================================================================
# This file is sourced by deploy.sh, add-node.sh, and remove-node.sh.
# Do NOT run this file directly.
# ============================================================================

TEMPMAIL_VERSION="3.0.0"
SCRIPT_START_TIME=$(date +%s)

# --- Color and UI Definitions ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# --- Logging Functions ---
log_info()    { echo -e "  ${CYAN}ℹ${NC}  $1"; }
log_success() { echo -e "  ${GREEN}✓${NC}  $1"; }
log_warn()    { echo -e "  ${YELLOW}⚠${NC}  $1"; }
log_error()   { echo -e "  ${RED}✗${NC}  $1"; exit 1; }
log_step()    { echo -e "\n${MAGENTA}${BOLD}━━━ $1 ━━━${NC}"; }

# --- Error trap handler ---
on_error() {
    local exit_code=$?
    local line_no=$1
    echo -e "\n${RED}${BOLD}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${RED}${BOLD}║  ❌  AN ERROR OCCURRED                                   ║${NC}"
    echo -e "${RED}${BOLD}╠══════════════════════════════════════════════════════════╣${NC}"
    echo -e "${RED}${BOLD}║  Line: ${line_no}  |  Exit Code: ${exit_code}                          ║${NC}"
    echo -e "${RED}${BOLD}╚══════════════════════════════════════════════════════════╝${NC}"
    echo -e "${YELLOW}  Check the output above for details.${NC}"
    echo -e "${DIM}  If this is a bug, report it with the full output.${NC}\n"
    exit "$exit_code"
}
trap 'on_error $LINENO' ERR

# --- Banner ---
print_banner() {
    local subtitle="${1:-}"
    clear
    echo -e "${CYAN}${BOLD}"
    cat << "BANNER"
  _____                   __  __       _ _
 |_   _|__ _ __ ___  _ __|  \/  | __ _(_) |
   | |/ _ \ '_ ` _ \| '_ \ |\/| |/ _` | | |
   | |  __/ | | | | | |_) | |  | | (_| | | |
   |_|\___|_| |_| |_| .__/|_|  |_|\__,_|_|_|
                     |_|
BANNER
    if [[ -n "$subtitle" ]]; then
        echo -e "      ${subtitle}"
    fi
    echo -e "${NC}${DIM}  Version ${TEMPMAIL_VERSION}${NC}"
    echo -e "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"
}

# --- Print elapsed time ---
print_elapsed() {
    local end_time=$(date +%s)
    local elapsed=$((end_time - SCRIPT_START_TIME))
    local minutes=$((elapsed / 60))
    local seconds=$((elapsed % 60))
    echo -e "\n${DIM}  ⏱  Completed in ${minutes}m ${seconds}s${NC}\n"
}

# --- Help flag handler ---
show_help() {
    local script_name="$1"
    local description="$2"
    echo -e "${BOLD}TempMail Platform v${TEMPMAIL_VERSION}${NC}"
    echo -e "${description}\n"
    echo -e "${BOLD}Usage:${NC}"
    echo -e "  chmod +x ${script_name}"
    echo -e "  ./${script_name} [OPTIONS]\n"
    echo -e "${BOLD}Options:${NC}"
    echo -e "  --help       Show this help message"
    echo -e "  --version    Show version information"
    if [[ "$script_name" == "remove-node.sh" ]]; then
        echo -e "  --force      Skip all confirmation prompts"
    fi
    echo ""
    exit 0
}

# --- Version flag handler ---
show_version() {
    echo "TempMail Platform v${TEMPMAIL_VERSION}"
    exit 0
}

# --- Parse common flags ---
parse_flags() {
    local script_name="$1"
    local description="$2"
    shift 2
    for arg in "$@"; do
        case "$arg" in
            --help|-h)    show_help "$script_name" "$description" ;;
            --version|-v) show_version ;;
        esac
    done
}

# --- Confirm prompt (Y/n) ---
confirm_action() {
    local prompt="$1"
    local default="${2:-Y}"
    local response
    read -rp "$(echo -e "${CYAN}? ${prompt} [${default}]: ${NC}")" response
    response=${response:-$default}
    [[ "$response" =~ ^[Yy]$ ]]
}

# --- Check running as root ---
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_warn "Running without root privileges."
        log_info "You may be prompted for your password."
        SUDO="sudo"
    else
        SUDO=""
    fi
}

# --- Generate Secure Random Token ---
generate_token() {
    local length=$1
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex "$length" | cut -c1-"$length"
    else
        head -c "$length" /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w "$length" | head -n 1
    fi
}

# --- Get Public IP ---
get_public_ip() {
    curl -s --max-time 5 ifconfig.me 2>/dev/null \
        || curl -s --max-time 5 api.ipify.org 2>/dev/null \
        || curl -s --max-time 5 icanhazip.com 2>/dev/null \
        || echo "YOUR_SERVER_IP"
}

# --- Detect OS family ---
detect_os() {
    if [ -f /etc/debian_version ]; then
        echo "debian"
    elif [ -f /etc/redhat-release ]; then
        echo "rhel"
    elif [ -f /etc/arch-release ]; then
        echo "arch"
    elif [ -f /etc/alpine-release ]; then
        echo "alpine"
    else
        echo "unknown"
    fi
}

# --- Install prerequisites ---
setup_os_prerequisites() {
    log_step "System Prerequisites"

    if command -v curl >/dev/null 2>&1 && command -v openssl >/dev/null 2>&1; then
        log_success "Required packages are already installed."
        return
    fi

    log_info "Installing required packages..."
    local os=$(detect_os)
    case "$os" in
        debian)  $SUDO apt-get update -yqq && $SUDO apt-get install -yqq curl git openssl ;;
        rhel)
            if command -v dnf >/dev/null 2>&1; then
                $SUDO dnf install -y curl git openssl
            else
                $SUDO yum install -y curl git openssl
            fi ;;
        arch)    $SUDO pacman -Sy --noconfirm curl git openssl ;;
        alpine)  $SUDO apk update && $SUDO apk add curl git openssl ;;
        *)       log_warn "Unknown OS. Attempting to continue..." ;;
    esac
    log_success "Prerequisites installed."
}

# --- Install Docker & Docker Compose ---
setup_docker() {
    log_step "Docker & Docker Compose"

    # Docker Engine
    if command -v docker >/dev/null 2>&1; then
        log_success "Docker: $(docker --version | head -1)"
    else
        log_warn "Docker not found. Installing..."
        curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
        $SUDO sh /tmp/get-docker.sh
        rm -f /tmp/get-docker.sh
        $SUDO systemctl enable --now docker || log_warn "Could not auto-enable docker service."
        if [[ $EUID -ne 0 ]]; then
            $SUDO usermod -aG docker "$USER" || true
        fi
        log_success "Docker installed successfully."
    fi

    # Docker Compose
    if docker compose version >/dev/null 2>&1; then
        log_success "Compose: $(docker compose version | head -1)"
    else
        log_warn "Docker Compose plugin not found. Installing..."
        local os=$(detect_os)
        case "$os" in
            debian) $SUDO apt-get update -yqq && $SUDO apt-get install -yqq docker-compose-plugin ;;
            rhel)
                if command -v dnf >/dev/null 2>&1; then
                    $SUDO dnf install -y docker-compose-plugin
                else
                    $SUDO yum install -y docker-compose-plugin
                fi ;;
            *)
                log_info "Downloading Compose plugin binary..."
                local arch
                arch=$(uname -m)
                case "$arch" in
                    x86_64)  arch="x86_64" ;;
                    aarch64) arch="aarch64" ;;
                    armv7l)  arch="armv7" ;;
                    *)       log_error "Unsupported architecture: $arch" ;;
                esac
                DOCKER_CONFIG=${DOCKER_CONFIG:-$HOME/.docker}
                mkdir -p "$DOCKER_CONFIG/cli-plugins"
                curl -SL "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-${arch}" \
                    -o "$DOCKER_CONFIG/cli-plugins/docker-compose"
                chmod +x "$DOCKER_CONFIG/cli-plugins/docker-compose"
                ;;
        esac

        if docker compose version >/dev/null 2>&1; then
            log_success "Docker Compose installed."
        else
            log_error "Failed to install Docker Compose. Please install it manually."
        fi
    fi
}

# --- Setup Dockge (auto-install) ---
setup_dockge() {
    log_step "Dockge (Docker UI Manager)"

    if [ -d "/opt/dockge" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^dockge$"; then
        log_success "Dockge is already installed."
        return
    fi

    log_info "Installing Dockge — lightweight web UI for managing Docker Compose stacks..."
    $SUDO mkdir -p /opt/dockge /opt/stacks
    cd /opt/dockge
    log_info "Downloading official Dockge compose file..."
    $SUDO curl -sL "https://dockge.kuma.pet/compose.yaml?port=5001&stacksPath=%2Fopt%2Fstacks" -o compose.yaml
    log_info "Starting Dockge..."
    $SUDO docker compose up -d
    cd - > /dev/null
    log_success "Dockge is running on port 5001."
}
