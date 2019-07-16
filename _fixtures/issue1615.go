package main

var strings = []string{
	"one",
	"two",
	"three",
	"four",
	"projects/my-gcp-project-id-string/locations/us-central1/queues/my-task-queue-name",
	"five",
	"six",
}

func f(s string) {
	// ...
}

func main() {
	for _, s := range strings {
		f(s)
	}
}
