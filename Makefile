default: build/dogetracker

.PHONY: clean, test
clean:
	rm -rf ./build

build/dogetracker: clean
	mkdir -p build
	go build -o build/dogetracker ./server/main.go

test:
	go test -v ./test/follower
