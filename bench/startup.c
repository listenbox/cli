// Linux startup benchmark: no shell, HTTP, JSON response decoding, or terminal rendering.
// Build: cc -O2 -Wall -Wextra -Werror bench/startup.c -o /tmp/cli-startup
// Run: taskset -c CPU /tmp/cli-startup /absolute/go-cli /absolute/rust-cli > samples.csv
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
extern char **environ;

static double micros(struct timespec time) {
    return time.tv_sec * 1000000.0 + time.tv_nsec / 1000.0;
}
static double cpu_micros(struct timeval time) {
    return time.tv_sec * 1000000.0 + time.tv_usec;
}
static void sample(char *binary, const char *label, int scenario, int iteration) {
    char *root[] = {binary, "--help", NULL};
    char *episode[] = {binary, "episodes", "create", "--help", NULL};
    posix_spawn_file_actions_t actions;
    if (posix_spawn_file_actions_init(&actions) ||
        posix_spawn_file_actions_addopen(&actions, STDIN_FILENO, "/dev/null", O_RDONLY, 0) ||
        posix_spawn_file_actions_addopen(&actions, STDOUT_FILENO, "/dev/null", O_WRONLY, 0) ||
        posix_spawn_file_actions_addopen(&actions, STDERR_FILENO, "/dev/null", O_WRONLY, 0)) {
        fprintf(stderr, "cannot configure benchmark descriptors\n"); exit(1);
    }
    pid_t child;
    struct timespec begin, end;
    struct rusage usage;
    int status;
    clock_gettime(CLOCK_MONOTONIC, &begin);
    int error = posix_spawn(&child, binary, &actions, NULL, scenario ? episode : root, environ);
    if (error) { fprintf(stderr, "spawn %s: %s\n", binary, strerror(error)); exit(1); }
    while (wait4(child, &status, 0, &usage) < 0) {
        if (errno != EINTR) { perror("wait4"); exit(1); }
    }
    clock_gettime(CLOCK_MONOTONIC, &end);
    posix_spawn_file_actions_destroy(&actions);
    if (!WIFEXITED(status) || WEXITSTATUS(status)) {
        fprintf(stderr, "%s scenario %d failed: status %d\n", binary, scenario, status); exit(1);
    }
    if (iteration >= 0) {
        printf("%s,%s,%d,%.3f,%.3f,%.3f,%ld,%ld,%ld,%ld,%ld\n", label,
            scenario ? "episodes-create-help" : "root-help", iteration,
            micros(end)-micros(begin), cpu_micros(usage.ru_utime), cpu_micros(usage.ru_stime),
            usage.ru_maxrss, usage.ru_minflt, usage.ru_majflt, usage.ru_nvcsw, usage.ru_nivcsw);
    }
}
int main(int argc, char **argv) {
    if (argc != 3) { fprintf(stderr, "usage: %s GO_BINARY RUST_BINARY\n", argv[0]); return 1; }
    puts("implementation,command,sample,wall_us,user_us,system_us,peak_rss_kib,minor_faults,major_faults,voluntary_context_switches,involuntary_context_switches");
    for (int scenario = 0; scenario < 2; scenario++) {
        // Alternating order counters drift. Both executables get 30 discarded warmups.
        for (int i = -30; i < 300; i++) {
            for (int order = 0; order < 2; order++) {
                int impl = (i + 30 + order) % 2;
                sample(argv[impl + 1], impl ? "rust" : "go", scenario, i);
            }
        }
    }
    return 0;
}
