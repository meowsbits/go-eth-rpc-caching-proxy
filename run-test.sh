#!/usr/bin/env bash

env ORIGIN_URL=$RIVET_ETC go test -count 1 -v .
