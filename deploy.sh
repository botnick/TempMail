#!/usr/bin/env bash
# ============================================================================
# TempMail Platform — Universal One-Click Deploy Script
# ============================================================================
# Supported OS: Ubuntu, Debian, CentOS, RHEL, Fedora, Rocky, AlmaLinux
# Usage:
#   chmod +x deploy.sh
#   ./deploy.sh
# ============================================================================

set -euo pipefail

# --- Color and UI Definitions ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

# --- Logging Functions ---
log_info()    { echo -e "${CYAN}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARNING]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }
log_step()    { echo -e "\n${MAGENTA}${BOLD}==> $1${NC}"; }

# --- Logo Header ---
print_banner() {
    clear
    echo -e "${CYAN}${BOLD}"
    cat << "EOF"
  _____                   __  __       _ _ 
 |_   _|__ _ __ ___  _ __|  \/  | __ _(_) |
   | |/ _ \ '_ ` _ \| '_ \ |\/| |/ _` | | |
   | |  __/ | | | | | |_) | |  | | (_| | | |
   |_|\___|_| |_| |_| .__/|_|  |_|\__,_|_|_|
                    |_|                     
EOF
    echo -e "      Universal Deployment Script      "
    echo -e "${NC}====================================================\n"
}

# --- Helper: Check running as root ---
check_root() {
    if [[ $EUID -ne 0 ]]; then
       log_warn "This script should ideally be run as root or with sudo privileges."
       log_info "Attempting to continue. You may be prompted for your password."
       SUDO="sudo"
    else
       SUDO=""
    fi
}

# --- Helper: Generate Secure Random Token ---
generate_token() {
    local length=$1
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex "$length" | cut -c1-"$length"
    else
        head -c "$length" /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w "$length" | head -n 1
    fi
}

# --- Check OS and Install Prerequisites (curl, etc) ---
setup_os_prerequisites() {
    log_step "Checking OS Prerequisites"
    if command -v curl >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
        log_success "Basic prerequisites (curl, git) are already installed."
        return
    fi

    log_info "Installing required basic packages..."
    if [ -f /etc/debian_version ]; then
        $SUDO apt-get update -yqq && $SUDO apt-get install -yqq curl git openssl xxd
    elif [ -f /etc/redhat-release ]; then
        if command -v dnf >/dev/null 2>&1; then
            $SUDO dnf install -y curl git openssl vim-common
        else
            $SUDO yum install -y curl git openssl vim-common
        fi
    elif [ -f /etc/arch-release ]; then
        $SUDO pacman -Sy --noconfirm curl git openssl
    elif [ -f /etc/alpine-release ]; then
        $SUDO apk update && $SUDO apk add curl git openssl
    else
        log_warn "Unsupported or unknown OS. Will try to proceed anyway, but things might break."
    fi
    log_success "Prerequisites installed."
}

# --- Verify or Install Docker & Docker Compose ---
setup_docker() {
    log_step "Checking Docker & Docker Compose"
    
    local install_docker=false

    # Check Docker
    if command -v docker >/dev/null 2>&1; then
        log_success "Docker is already installed: $(docker --version)"
    else
        log_warn "Docker not found."
        install_docker=true
    fi

    # Install Docker if needed
    if [ "$install_docker" = true ]; then
        log_info "Installing Docker via Official Universal Script..."
        curl -fsSL https://get.docker.com -o get-docker.sh
        $SUDO sh get-docker.sh
        rm -f get-docker.sh
        
        $SUDO systemctl enable --now docker || log_warn "Could not enable docker service automatically."
        
        # Give user permission if running via sudo but not as root (optional but helpful)
        if [[ $EUID -ne 0 ]]; then
            $SUDO usermod -aG docker $USER || true
            log_warn "Added user to docker group. You may need to logout and back in later to run docker commands without sudo."
        fi
        log_success "Docker installed successfully."
    fi

    # Check Docker Compose (v2 Plugin preferred, standalone fallback)
    if docker compose version >/dev/null 2>&1; then
        log_success "Docker Compose plugin is available: $(docker compose version)"
    else
        log_warn "Docker Compose plugin not found. Attempting to install..."
        if [ -f /etc/debian_version ]; then
            $SUDO apt-get update -yqq && $SUDO apt-get install -yqq docker-compose-plugin
        elif [ -f /etc/redhat-release ]; then
            if command -v dnf >/dev/null 2>&1; then
                $SUDO dnf install -y docker-compose-plugin
            else
                $SUDO yum install -y docker-compose-plugin
            fi
        else
            log_info "Falling back to downloading binary plugin manually..."
            DOCKER_CONFIG=${DOCKER_CONFIG:-$HOME/.docker}
            mkdir -p $DOCKER_CONFIG/cli-plugins
            curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64 -o $DOCKER_CONFIG/cli-plugins/docker-compose
            chmod +x $DOCKER_CONFIG/cli-plugins/docker-compose
        fi
        
        if docker compose version >/dev/null 2>&1; then
            log_success "Docker Compose installed."
        else
            log_error "Failed to install Docker Compose. Please install it manually."
        fi
    fi
}

# --- Collect Configuration Input ---
collect_configuration() {
    log_step "Configuration Setup"
    
    # 1. Domain
    read -rp "$(echo -e ${CYAN}"? Enter your primary mail domain (e.g., mail.example.com): "${NC})" MAIL_DOMAIN
    if [[ -z "$MAIL_DOMAIN" ]]; then log_error "Domain cannot be empty."; fi

    # 2. Frontend URL
    read -rp "$(echo -e ${CYAN}"? Enter Frontend URL (optional, e.g., https://app.example.com): "${NC})" FRONTEND_URL

    # 3. Cloudflare R2
    echo -e "\n${YELLOW}Cloudflare R2 Storage Configuration (Attachments/Archives)${NC}"
    read -rp "$(echo -e ${CYAN}"  > R2 Account ID: "${NC})" R2_ACCOUNT_ID
    read -rp "$(echo -e ${CYAN}"  > R2 Access Key ID: "${NC})" R2_ACCESS_KEY_ID
    read -rsp "$(echo -e ${CYAN}"  > R2 Secret Access Key: "${NC})" R2_SECRET_ACCESS_KEY
    echo ""
    read -rp "$(echo -e ${CYAN}"  > R2 Bucket Name [tempmail-archives]: "${NC})" R2_BUCKET_NAME
    R2_BUCKET_NAME=${R2_BUCKET_NAME:-tempmail-archives}
}

# --- Generate Security Tokens ---
generate_secrets() {
    log_step "Generating Security Tokens"
    
    POSTGRES_PASSWORD=$(generate_token 24)
    REDIS_PASSWORD=$(generate_token 24)
    INTERNAL_API_TOKEN=$(generate_token 32)
    EXTERNAL_API_KEY=$(generate_token 32)
    ADMIN_API_KEY=$(generate_token 32)
    ADMIN_PANEL_PATH=$(generate_token 16)

    log_success "Database & Cache passwords created securely."
    log_success "API and Admin access tokens generated."
}

# --- Generate .env File ---
create_env_file() {
    log_step "Writing Environment Configuration"
    
    cat > .env << EOF
# Auto-generated by deploy.sh on $(date -u +"%Y-%m-%dT%H:%M:%SZ")
# ========================================

# Database
POSTGRES_USER=tempmail
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
POSTGRES_DB=tempmail_db
DATABASE_URL=host=postgres user=tempmail password=${POSTGRES_PASSWORD} dbname=tempmail_db port=5432 sslmode=disable TimeZone=UTC

# Redis
REDIS_PASSWORD=${REDIS_PASSWORD}
REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379

# Security Tokens
INTERNAL_API_TOKEN=${INTERNAL_API_TOKEN}
EXTERNAL_API_KEY=${EXTERNAL_API_KEY}
ADMIN_API_KEY=${ADMIN_API_KEY}
ADMIN_PANEL_PATH=${ADMIN_PANEL_PATH}

# Cloudflare R2
R2_ACCOUNT_ID=${R2_ACCOUNT_ID}
R2_ACCESS_KEY_ID=${R2_ACCESS_KEY_ID}
R2_SECRET_ACCESS_KEY=${R2_SECRET_ACCESS_KEY}
R2_BUCKET_NAME=${R2_BUCKET_NAME}

# Spam
RSPAMD_URL=http://rspamd:11333
RSPAMD_PASSWORD=
SPAM_REJECT_THRESHOLD=15

# Frontend (optional — only needed if browsers call API directly)
FRONTEND_URL=${FRONTEND_URL}

# Limits
MAX_MESSAGE_SIZE_MB=25
MAX_ATTACHMENTS=10
MAX_ATTACHMENT_SIZE_MB=10

# Logging
LOG_LEVEL=info
LOG_MAX_AGE_DAYS=14
LOG_MAX_SIZE_MB=100
LOG_MAX_BACKUPS=10
EOF

    log_success ".env file written to disk."
}

# --- Install and Setup Dockge ---
setup_dockge() {
    log_step "Checking Dockge (Docker UI Manager)"
    
    # Check if Dockge is already running or directory exists
    if [ -d "/opt/dockge" ] || docker ps --format '{{.Names}}' | grep -q "^dockge$"; then
        log_success "Dockge is already installed or running."
        return
    fi
    
    echo -e "${YELLOW}Dockge is a lightweight, easy-to-use Docker orchestrator/UI.${NC}"
    read -rp "$(echo -e ${CYAN}"? Do you want to install Dockge to manage your containers? [Y/n]: "${NC})" INSTALL_DOCKGE
    INSTALL_DOCKGE=${INSTALL_DOCKGE:-Y}
    
    if [[ "$INSTALL_DOCKGE" =~ ^[Yy]$ ]]; then
        log_info "Installing Dockge..."
        
        # Create directories
        $SUDO mkdir -p /opt/dockge
        $SUDO mkdir -p /opt/stacks
        cd /opt/dockge
        
        # Download official dockge compose file
        log_info "Downloading Dockge compose file..."
        $SUDO curl -sL "https://dockge.kuma.pet/compose.yaml?port=5001&stacksPath=%2Fopt%2Fstacks" -o compose.yaml
        
        # Start Dockge
        log_info "Starting Dockge container..."
        $SUDO docker compose up -d
        
        # Return to previous directory
        cd - > /dev/null
        
        log_success "Dockge installed successfully. Stacks path: /opt/stacks"
    else
        log_info "Skipping Dockge installation."
    fi
}

# --- Docker Compose Build & Run ---
deploy_services() {
    log_step "Building & Starting Mailserver Services"
    
    # Ensure stacks dir exists regardless (for safety)
    $SUDO mkdir -p /opt/stacks

    log_info "Building containers. This might take a few minutes..."
    $SUDO docker compose build --no-cache

    log_info "Starting containers..."
    $SUDO docker compose up -d

    log_success "All mailserver services have been started successfully."
}

# --- Main Flow ---
main() {
    print_banner
    check_root
    setup_os_prerequisites
    setup_docker
    setup_dockge
    collect_configuration
    generate_secrets
    create_env_file
    deploy_services

    # --- Print Summary ---
    log_step "DEPLOYMENT COMPLETE"
    
    # Try to grab public IP
    PUBLIC_IP=$(curl -s ifconfig.me || echo "YOUR_SERVER_IP")

    echo -e "${CYAN}=== Connection Details ===${NC}"
    echo -e "  API URL:           ${GREEN}http://localhost:4000${NC}"
    echo -e "  SMTP Inbound:      ${GREEN}${MAIL_DOMAIN}:25${NC}"
    echo -e "  Rspamd UI:         ${GREEN}http://localhost:11334${NC}"
    echo ""
    echo -e "${CYAN}=== Web App Integration ===${NC}"
    echo -e "  ${YELLOW}Copy these values to your Frontend ENV:${NC}"
    echo -e "  API_URL:           ${GREEN}http://${PUBLIC_IP}:4000${NC}"
    echo -e "  API_KEY:           ${GREEN}${EXTERNAL_API_KEY}${NC}"
    echo ""
    echo -e "${CYAN}=== Admin Access ===${NC}"
    echo -e "  ADMIN_KEY:         ${GREEN}${ADMIN_API_KEY}${NC}"
    echo -e "  ADMIN_PANEL_URL:   ${GREEN}http://localhost:4000/${ADMIN_PANEL_PATH}/${NC}"
    echo ""
    echo -e "${CYAN}=== Docker Container Management ===${NC}"
    echo -e "  Dockge UI:         ${GREEN}http://${PUBLIC_IP}:5001${NC}"
    echo -e "  ${YELLOW}(Create an admin account on your first visit)${NC}"
    echo ""
    echo -e "${YELLOW}=== 🌐 DNS CONFIGURATION REQUIRED ===${NC}"
    echo -e "  Add these DNS records for ${MAIL_DOMAIN}:"
    echo -e "    MX   ${MAIL_DOMAIN}  →  ${MAIL_DOMAIN}  (priority 10)"
    echo -e "    A    ${MAIL_DOMAIN}  →  ${PUBLIC_IP}"
    echo ""
    echo -e "${RED}${BOLD}⚠ DANGER: Save these keys securely right now! They will not be shown again.${NC}"
    echo ""
}

# Run the script
main
