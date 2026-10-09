#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

// Loads a Go shared library, then, for every line read from stdin, loads
// the library named in argv[2] and calls into the Go library. This allows a
// debugger to attach after the Go library has been loaded, and to observe a
// shared library being loaded afterwards.
int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <path-to-go-shared-lib> <other-lib>\n", argv[0]);
        return 1;
    }

    void *handle = dlopen(argv[1], RTLD_NOW);
    if (!handle) {
        fprintf(stderr, "dlopen failed: %s\n", dlerror());
        return 1;
    }

    int (*goFunc)() = (int (*)())dlsym(handle, "GoFunction");
    if (!goFunc) {
        fprintf(stderr, "dlsym failed: %s\n", dlerror());
        return 1;
    }

    printf("ready\n");
    fflush(stdout);

    char line[64];
    while (fgets(line, sizeof(line), stdin)) {
        if (!dlopen(argv[2], RTLD_NOW)) {
            fprintf(stderr, "dlopen failed: %s\n", dlerror());
            return 1;
        }
        printf("result: %d\n", goFunc());
        fflush(stdout);
    }
    return 0;
}
