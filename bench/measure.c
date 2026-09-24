// Linux process accounting for one real CLI invocation. The E2E harness owns
// fixtures and cancellation; this child retains its supervisor's process group.
#define _GNU_SOURCE
#include <errno.h>
#include <spawn.h>
#include <stdio.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <time.h>

extern char **environ;

static double milliseconds(struct timespec value) {
    return value.tv_sec * 1000.0 + value.tv_nsec / 1000000.0;
}

static double cpu_ms(struct timeval value) {
    return value.tv_sec * 1000.0 + value.tv_usec / 1000.0;
}

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s METRICS_JSON BINARY [ARGS...]\n", argv[0]);
        return 1;
    }
    struct timespec begin, end;
    struct rusage usage;
    pid_t child;
    int status;
    if (clock_gettime(CLOCK_MONOTONIC, &begin)) { perror("clock"); return 1; }
    int error = posix_spawn(&child, argv[2], NULL, NULL, argv + 2, environ);
    if (error) { fprintf(stderr, "spawn: %s\n", strerror(error)); return 1; }
    while (wait4(child, &status, 0, &usage) < 0) {
        if (errno != EINTR) { perror("wait4"); return 1; }
    }
    if (clock_gettime(CLOCK_MONOTONIC, &end)) { perror("clock"); return 1; }
    if (!WIFEXITED(status) || WEXITSTATUS(status)) {
        fprintf(stderr, "CLI failed: wait status %d\n", status);
        return 1;
    }
    FILE *output = fopen(argv[1], "wx");
    if (!output) { perror("metrics file"); return 1; }
    double wall = milliseconds(end) - milliseconds(begin);
    double cpu = cpu_ms(usage.ru_utime) + cpu_ms(usage.ru_stime);
    int written = fprintf(output,
        "{\"wall_ms\":%.6f,\"cpu_ms\":%.3f,\"cpu_percent\":%.6f,\"peak_rss_mib\":%.6f}\n",
        wall, cpu, 100.0 * cpu / wall, usage.ru_maxrss / 1024.0);
    int closed = fclose(output);
    if (written < 0 || closed) { perror("write metrics"); return 1; }
    return 0;
}
