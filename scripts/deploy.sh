#!/bin/bash
set -e

SERVER_USER="ec2-user"
SERVER_IP="13.234.34.188"
SERVER_DIR="~/Approx/server"
PEM="$(dirname "$0")/../server/LightsailDefaultKey-ap-south-1.pem"
SRC_DIR="$(dirname "$0")/../server"

SSH="ssh -i $PEM $SERVER_USER@$SERVER_IP"
SCP="scp -i $PEM"

echo "[build] Cross-compiling for Linux..."
cd "$SRC_DIR"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o approx-server .
echo "[build] Done — $(du -sh approx-server | cut -f1) binary"

echo "[scp] Uploading binary..."
$SCP approx-server $SERVER_USER@$SERVER_IP:$SERVER_DIR/

echo "[scp] Uploading static files..."
$SCP -r static $SERVER_USER@$SERVER_IP:$SERVER_DIR/

echo "[scp] Done."

echo "[server] Restarting..."
$SSH "pkill -f approx-server || true; sleep 1; cd $SERVER_DIR && set -a && source .env && set +a && nohup ./approx-server > ~/approx.log 2>&1 & sleep 1 && curl -s localhost:8080/health"

echo ""
echo "Deployed to http://$SERVER_IP:8080"
