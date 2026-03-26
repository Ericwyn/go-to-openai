#!/bin/bash
#CONFIG_FILE=./config.json sudo ./go-to-openai
go build . && sudo ./go-to-openai -config="./config.json"
