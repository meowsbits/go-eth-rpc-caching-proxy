#!/usr/bin/env bash

# gcloud app deploy --project=rivet-proxy-v1

set -e

mkdir -p ./build/bin
go build -o ./build/bin/rpc2influx ./v2
chmod +x ./build/bin/rpc2influx
rsync -avz ./build/bin/rpc2influx coop-do-ethercluster-metrics:/usr/local/bin/

