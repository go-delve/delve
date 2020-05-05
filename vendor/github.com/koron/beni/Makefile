SUBDIRS = ./token ./lexer ./theme ./formatter . ./cmd/beni

test:
	go test $(SUBDIRS)

tags:
	ctags -R --exclude=tmp .

clean:
	rm -f beni beni.exe
	rm -f tags

.PHONY: tags
