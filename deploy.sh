#!/bin/bash
# Deploy script for go-whatsapp-web-multidevice
# Usage: ./deploy.sh

set -e

SERVER="root@185.225.233.92"
IMAGE_NAME="logimacro/go-whatsapp:latest"
CONTAINER_NAME="go-whatsapp"
NETWORK="router-manager_router-network"
TMP_FILE="/tmp/go-whatsapp-latest.tar.gz"

echo "🔨 [1/5] Building Docker image..."
docker build -f docker/golang.Dockerfile -t $IMAGE_NAME .

echo "💾 [2/5] Saving image to file..."
docker save $IMAGE_NAME | gzip > $TMP_FILE
echo "   Image size: $(ls -lh $TMP_FILE | awk '{print $5}')"

echo "📤 [3/5] Transferring to server..."
scp $TMP_FILE $SERVER:/tmp/

echo "📥 [4/5] Loading image on server..."
ssh $SERVER "docker load < $TMP_FILE"

echo "🔄 [5/5] Restarting container..."
ssh $SERVER "
  docker stop $CONTAINER_NAME 2>/dev/null || true
  docker rm $CONTAINER_NAME 2>/dev/null || true
  docker run -d \
    --name $CONTAINER_NAME \
    --network $NETWORK \
    -p 3007:3000 \
    -v whatsapp_data:/app/storages \
    -v /root/go-whatsapp/statics:/app/statics \
    -v /root/go-whatsapp/.env:/app/.env:ro \
    --restart unless-stopped \
    $IMAGE_NAME
"

echo "✅ Deploy complete!"
ssh $SERVER "docker logs $CONTAINER_NAME --tail 5"

# Cleanup
rm -f $TMP_FILE
