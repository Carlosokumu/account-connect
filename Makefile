APP_NAME := account-connect

PROTO_DIR := proto   
PROTO_FILES := $(shell find $(PROTO_DIR) -name '*.proto')       
GEN_DIR := gen               
PROTOC_GEN_GO := $(shell which protoc-gen-go)
PROTOC_GEN_GO_GRPC := $(shell which protoc-gen-go-grpc)
PROTOC := protoc


all: proto build



proto: $(PROTO_FILES)
	@echo "Generating code for: $(PROTO_FILES)"
	@mkdir -p $(GEN_DIR)
	cd $(PROTO_DIR) && \
	$(PROTOC) --go_out=../$(GEN_DIR) --go_opt=paths=source_relative \
	          --go-grpc_out=../$(GEN_DIR) --go-grpc_opt=paths=source_relative \
	          *.proto

build:
	go build -o $(APP_NAME) main.go

run: build
	./$(APP_NAME)

clean:
	rm -rf $(GEN_DIR) $(APP_NAME)


setup:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest