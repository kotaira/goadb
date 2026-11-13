#goadb

[![Build Status](https://travis-ci.org/zach-klippenstein/goadb.svg?branch=master)](https://travis-ci.org/zach-klippenstein/goadb)
[![GoDoc](https://godoc.org/github.com/zach-klippenstein/goadb?status.svg)](https://godoc.org/github.com/zach-klippenstein/goadb)

A Golang library for interacting with the Android Debug Bridge (adb).

See [demo.go](cmd/demo/demo.go) for usage.


## Persistent shell

`OpenShell()` opens one interactive ADB shell. All writes use that same session,
so `cd`, variables, and shell state persist between commands. For example:

```go
shell, err := device.OpenShell()
if err != nil {
    return err
}
defer shell.Close()

// Read continuously: command output isn't split into request/response messages.
outputDone := make(chan error, 1)
go func() {
    _, err := io.Copy(os.Stdout, shell.GetReader())
    outputDone <- err
}()

for _, command := range []string{"cd /data/local/tmp", "pwd", "cat example.txt", "exit"} {
    if _, err := io.WriteString(shell.GetWriter(), command+"\n"); err != nil {
        return err
    }
}
return <-outputDone
```

Use one reader and one writer. Write raw input with `GetWriter()`, not
`SendMessage()` (which adds ADB protocol framing). The legacy interactive shell
may echo input and print prompts; stdout and stderr are combined, and per-command
exit codes aren't provided. This API doesn't configure a local terminal's raw
mode or terminal resize events. Disconnects end the session; opening a new one
starts with new shell state. Existing `RunCommand` and `RunCommandWithShell`
remain available for single commands.
