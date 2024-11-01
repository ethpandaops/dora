
all: install test build

dep_npm:
	if [ -z "$(shell which npm)" ]; then echo "npm is not installed"; exit 1; fi

install: dep_npm
	npm install

test: dep_npm
	npm run test

build: dep_npm clean
	npm run build

debug-build: dep_npm clean
	DEBUG=1 npm run build

clean:
	rm -rf dist/*
