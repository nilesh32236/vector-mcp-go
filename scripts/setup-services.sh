#!/bin/bash

# Vector MCP Service Setup Script
# Run this as sudo if possible, or follow the manual steps below.

SERVICE_DIR="/etc/systemd/system"
PROJECT_DIR="/home/nilesh/Documents/vector-mcp-go"

echo "Configuring Vector MCP services..."

# Copy service files to systemd
sudo cp "$PROJECT_DIR/scripts/vector-mcp.service" "$SERVICE_DIR/"
sudo cp "$PROJECT_DIR/scripts/vector-mcp-ui.service" "$SERVICE_DIR/"

# Reload systemd daemon
sudo systemctl daemon-reload

# Enable services to start at boot
sudo systemctl enable vector-mcp
sudo systemctl enable vector-mcp-ui

# Start services immediately
sudo systemctl start vector-mcp
sudo systemctl start vector-mcp-ui

echo "Services have been configured and started."
echo "Check status:"
echo "systemctl status vector-mcp"
echo "systemctl status vector-mcp-ui"
