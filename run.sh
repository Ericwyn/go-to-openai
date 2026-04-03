#!/bin/bash

# Build the project
go build -o go-to-openai .

# 1. Generate root CA certificate
./go-to-openai root-crt-gen

# 2. Install root CA certificate to system trust store (requires sudo)
sudo ./go-to-openai root-crt-install

# 3. Generate domain certificates
./go-to-openai domain-crt-gen "api.openai.com"
./go-to-openai domain-crt-gen "api.anthropic.com"

# 4. Start the HTTPS proxy server (requires sudo for port 443)
sudo ./go-to-openai run -config="./config.json"
