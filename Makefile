.PHONY: build test race safe purego fuzz bench cli install clean

build:
	go build ./...

test:
	go vet ./...
	go test ./... -count=1

race:
	go test -race ./... -count=1

safe:
	go test -tags jseeksafe ./... -count=1

purego:
	go test -tags purego ./... -count=1

# Full matrix as CI runs it.
check: test race safe purego
	go test -tags 'purego jseeksafe' ./... -count=1
	go test -count=1 -run '^Fuzz' ./

# Short generative pass over every differential target.
fuzz:
	@for t in $$(grep -rhoE '^func (Fuzz[A-Za-z0-9]+)' --include='*_test.go' . | awk '{print $$2}' | sort -u); do \
		echo "== $$t"; \
		go test -run '^$$' -fuzz "^$$t$$" -fuzztime 20s ./ || exit 1; \
	done

bench:
	cd bench && go test -run '^$$' -bench=. -benchmem -count=6

cli:
	go build -trimpath -o jseek ./cmd/jseek

install:
	go install ./cmd/jseek

clean:
	rm -f jseek