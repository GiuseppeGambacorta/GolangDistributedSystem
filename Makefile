CERTS_DIR := security/certs
SECURITY_CONFIG_DIR := security/config
GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: init gencert compile test

init:
	mkdir -p $(CERTS_DIR)

gencert: init
	cfssl gencert \
		-initca $(SECURITY_CONFIG_DIR)/01-ca-identita.json | cfssljson -bare ca
	cfssl gencert \
		-ca=ca.pem \
		-ca-key=ca-key.pem \
		-config=$(SECURITY_CONFIG_DIR)/02-ca-regole-firma.json \
		-profile=server \
		$(SECURITY_CONFIG_DIR)/03-server-richiesta.json | cfssljson -bare server
	cfssl gencert \
		-ca=ca.pem \
		-ca-key=ca-key.pem \
		-config=$(SECURITY_CONFIG_DIR)/02-ca-regole-firma.json \
		-profile=client \
		$(SECURITY_CONFIG_DIR)/04-client-richiesta.json | cfssljson -bare client
	mv ca.pem $(CERTS_DIR)/ca-public-cert.pem
	mv ca-key.pem $(CERTS_DIR)/ca-private-key.pem
	mv server.pem $(CERTS_DIR)/server-public-cert.pem
	mv server-key.pem $(CERTS_DIR)/server-private-key.pem
	mv client.pem $(CERTS_DIR)/client-public-cert.pem
	mv client-key.pem $(CERTS_DIR)/client-private-key.pem
	rm -f ca.csr server.csr client.csr

compile:
	protoc api/v1/*.proto \
	--go_out=. \
	--go-grpc_out=. \
	--go_opt=paths=source_relative \
	--go-grpc_opt=paths=source_relative \
	--proto_path=.

test:
	go test -race ./...
