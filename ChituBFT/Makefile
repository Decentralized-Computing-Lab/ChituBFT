all:
	go build -o chitu
	$(MAKE) -C coordinator
	$(MAKE) -C client

clean:
	rm chitu
	$(MAKE) clean -C coordinator
	$(MAKE) clean -C client