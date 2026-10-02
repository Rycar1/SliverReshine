# Shellcode loader — verification tooling

Two small C programs used to answer one question: **does the shellcode this
console generates actually execute?**

They exist because building a payload and executing one are different claims.
The route sweep proved the API works; the callback test proved generated
*executables* call home. Neither touches shellcode, which is raw
position-independent code with no loader of its own — so "it built" says nothing
about whether it runs.

## What is here

| File | Purpose |
|---|---|
| `loader.c` | Loads a `.bin` into executable memory and calls it. This is the tool that verifies a payload. |
| `archprobe.c` | Answers "can this process run 32-bit code?". Needed to interpret a 386 result. |

## !! Read this before running anything

**`loader.exe` executes arbitrary code from a file. That is its entire function.**

- Run it **only inside a disposable VM or container**, never on a workstation
  you care about.
- The payload it loads is a real implant. Once `entry()` is called the process
  can open sockets, read files, and persist. It does not clean up after itself.
- Generated payloads connect to whatever C2 address is compiled into them. Bind
  your listeners to loopback (`127.0.0.1`) so nothing is exposed, and stop the
  listeners when you are done.
- Kill the loader process and delete the payload afterwards. A payload left
  running is a backdoor left running.
- AV/EDR will likely flag both the loader (self-injection) and the payload. That
  is expected; do not treat an alert as a false positive.

## Build

MSYS2 / MinGW gcc, no other dependencies:

```sh
gcc -O0 -o loader.exe loader.c
gcc -O0 -o archprobe.exe archprobe.c
```

The `.exe` outputs are build artefacts and are not committed.

## Use

```sh
# Confirm the loader can read the payload and map it, without running it
./loader.exe payload.bin --no-exec

# Run it
./loader.exe payload.bin
```

`loader.exe` prints each step (size, detected format, mapped address) and then
calls the payload. A payload that produces a session **does not return** — the
process stays alive, which is the expected outcome. If it prints
`the payload returned`, the bytes ran and finished, which is not what a
session-producing payload should do.

### Which helper to reach for

```sh
# Does this host run 32-bit shellcode at all?
./archprobe.exe x64    # control: valid in both modes, should return cleanly
./archprobe.exe x86    # uses PUSHAD (0x60), illegal in 64-bit mode
```

## Two traps this tooling was built to stop repeating

**1. A 64-bit loader cannot run 32-bit shellcode.**

`windows/386` shellcode will never call back when loaded from a 64-bit process,
because long mode does not decode 32-bit-only instructions. `archprobe` makes
that explicit: the x86 blob (`60 C3`, `PUSHAD; RET`) dies with
`0xC000001D` (illegal instruction) while the x64 control returns cleanly. Before
this was measured, a 386 payload that produced no callback looked like a
generated-payload defect. It was not — the test was invalid.

**2. A probe whose samples do not differ proves nothing.**

The first `archprobe` used `xor eax,eax; ret` for both blobs. That encoding is
valid in both modes, so both returned cleanly and the probe "passed" without
testing anything. The samples have to differ in something the decoder actually
rejects.

## Verified results

Measured with these tools against a locally generated payload set:

| Payload | Result |
|---|---|
| `windows/amd64` shellcode, mTLS | calls back |
| `windows/amd64` shellcode, HTTP | calls back |
| `windows/amd64` shellcode, evasion enabled | calls back |
| `windows/386` shellcode | **not testable here** — needs a 32-bit host |

The 386 row is a statement about the environment, not about the payload.
