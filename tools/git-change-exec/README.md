# git-change-exec

This new tool detects if in your git tree files changed
compared to:

* master branch
* stable branches

also local-only files are considered.

Here it is used to run pillar's go-tests only if something
changed there, same for this tool itself and the get-deps tool.

## Add new Action

Open `actions.go` add new implementation of `action` `interface` and
add it to the `actions` array.

Run `git-change-exec -d` to see if your action gets triggered without
running.

## Example output

```bash
2024/09/04 09:49:17 --- running gitChangeExecTest ...
=== RUN   TestId
--- PASS: TestId (0.00s)
PASS
ok  	git-change-exec	0.004s
2024/09/04 09:49:20 --- running gitChangeExecTest done
```
