# Windows terminal: three separate problems

A Windows 8.1 target reported three things at once:

1. The default shell produced `Microsoft Windows [汾 6.3.9600]` — mojibake.
2. Typing in cmd.exe showed nothing until Enter.
3. PowerShell showed nothing at all, and Enter did nothing either.

They look like one problem and are three, with three different causes and three
different fixes. This note records what each actually was, because the obvious
reading — "the terminal is broken" — leads to a single wrong fix.

## 1. Encoding: the shell tunnel did not transcode

`Microsoft Windows [版本 6.3.9600]` arrived as `[汾 6.3.9600]`. The text was not
merely ugly, it was destroyed: GBK bytes decoded as UTF-8 produce U+FFFD or an
unrelated glyph, and the original is unrecoverable from the output.

The cause is a disagreement between two modes on the same target. Exec mode
already decoded its output (`decodeWindowsOutput`, added when `reg`/`sc`/`net`
messages were being destroyed the same way). The shell tunnel did not — it
forwarded `tunnel.Read` bytes straight to the browser:

```go
n, err := tunnel.Read(buf)
if n > 0 {
    writeWS(ws, wsMsgData, buf[:n])   // raw OEM bytes, browser expects UTF-8
}
```

So `whoami` read correctly in compat mode and as mojibake in shell mode, on the
same session.

### The fix, and why it is not a one-liner

`ConsoleCodec` (`internal/ui/sliver/consoletranscode.go`) transcodes both
directions. Two things make it more than a `chcp`:

**It is streaming.** A code page like GBK is multi-byte, and `tunnel.Read`
returns arbitrary chunks — an 8192-byte boundary can fall inside a character.
Decoding each chunk independently corrupts exactly those characters, and *which*
ones depends on where the boundary landed. The codec holds an incomplete
trailing sequence until the next read completes it.

**Input needs the mirror.** When the operator types a non-ASCII character the
browser sends UTF-8, and a shell reading GBK from its pipe sees two or three
unrelated characters — so a path typed correctly names something that does not
exist. `Encode` does the reverse conversion.

### A trap the tests caught

The first version passed through anything that was already valid UTF-8, which is
what the existing whole-message decoder does. That is wrong for a stream, and the
split-point test proved it:

```
GBK  0xCF 0xA2  =  "息"
UTF-8 0xCF 0xA2  =  "Ϣ"   (U+03E2)
```

Those two bytes are valid in *both* encodings. A chunk that happened to end
right after them was emitted undecoded, so the last character of a message
became an unrelated glyph — at a rate determined by read sizes. The codec now
decodes unconditionally; passing through is decided by the code page itself (an
unknown page or 65001 yields a nil codec), not by inspecting bytes.

This is why `TestCodecSurvivesEverySplitPoint` walks every boundary rather than
spot-checking one.

## 2. cmd.exe: no per-keystroke echo, and why echoing locally is wrong

A console-less `cmd.exe` does not echo keystrokes — there is no console to do
it. It does, however, echo the *completed line* once it reads it, right after the
prompt. Verified by piping into it:

```
C:\Users\Rycar\AppData\Local\Temp>whoami<CR><LF>
rycarl\rycar<CR><LF>
```

So the operator sees nothing while typing, then the whole command at once. That
is the reported symptom, and it is the shell's own behaviour rather than a
console defect.

The obvious fix is local echo. It is wrong here: the shell already echoes the
line, so echoing locally too prints the command twice on one line —

```
C:\>whoamiwhoami
```

— which reads as a different command than the one that ran. A delayed echo is a
worse experience than an immediate one but a *doubled* echo is a wrong one, so no
local echo was added. `terminal.go` records this where a future reader would be
tempted to add it.

The real fix is a console, not an echo: ConPTY gives Windows a PTY, and Windows
10 1809+ has it. Windows 8.1 does not, so on those targets the shell reads from a
pipe and the choice is between a delayed echo and a doubled one.

## 3. PowerShell: silent, and the flag that caused it

The implant launched PowerShell with a `-Command` prologue that set
`[Console]::OutputEncoding` to UTF-8, wrapped in `try/catch`. The source comment
admitted it was never diagnosed:

> The try/catch is defensive, not diagnosed. It was added after a Windows 8
> target produced no terminal output while Windows 10 worked, and the flag set
> was the only difference between this and the cmd.exe that was never tried.

It was still failing. It is now removed, for two independent reasons:

- **It is the suspect.** Assigning `[Console]::OutputEncoding` recreates the
  console output stream, and with no console — exactly this case, a piped process
  started with `CREATE_NO_WINDOW` — that is a documented way to lose output
  entirely.
- **It is unnecessary, and now harmful.** The console transcodes the OEM code page
  itself, so a shell does not need to be talked into emitting UTF-8. Leaving the
  prologue in would make PowerShell emit UTF-8 while the console decoded it as
  GBK — mojibake in the opposite direction, introduced by fixing the first bug.

Verified locally that the remaining flags (`-NoLogo -NoExit -ExecutionPolicy
Bypass`) read a pipe correctly, and that `-Command -` also works. **Not verified
on Windows 8.1**, which is the platform that reported it — see the caveats.

## The horizontal sweep

The question worth asking is not "is the terminal fixed" but "where else does
target text reach the UI without being transcoded". Two classes of path exist,
and only one needs work:

| Source | Encoding | Needs transcoding |
|---|---|---|
| Output of a spawned console program (`cmd`, `reg`, `sc`, `net`) | OEM code page | **Yes** |
| Values the Go implant reads directly (files, processes, registry, env) | UTF-8 | No |

The distinction is what the implant does, not where the text ends up. Go reads
Windows APIs as UTF-16 and converts to UTF-8, so a filename, a process name or a
registry value is already correct by the time it reaches the console. A console
program's *output* is bytes in the console code page, and that is the only thing
that needs decoding.

Every path that spawns a program therefore has to decode. After this change:

| Path | Decodes |
|---|---|
| `execOn` — exec mode, persistence, mimikatz, ops | yes (pre-existing) |
| Shell tunnel — shell and shell-copy modes | yes (this change) |
| `Ls`, `Ps`, `Netstat`, `GetEnv`, `RegistryRead`, `Ifconfig` | not needed — Go reads them |

`execRawOn` is called in exactly one place, the code page probe itself, which
must not recurse into decoding.

## Not verified

- **Windows 8.1.** No access to that platform. The PowerShell fix is reasoned
  from the source comment and from the documented behaviour of the
  `[Console]::OutputEncoding` setter; it is not reproduced. If PowerShell is still
  silent there, the fallback in `StartInteractive` is the next place to look — it
  only triggers on a spawn *error*, and a shell that starts but never speaks
  produces neither an error nor output.
- **A real target for the codec.** The codec is unit-tested at every split point
  with GBK fixtures, but has not been exercised against a live Windows session.
- **Non-GBK code pages end to end.** 437, 850, 852, 866 and 1252 are wired up and
  covered by the decoder table, but only 936 has byte-level fixtures.
