<!--
Thanks for the change. The sections below are what reviewers read first; delete
any that genuinely do not apply.
-->

## The problem

<!-- What was wrong, or missing. Link the issue if there is one. -->

## The change

<!-- What you did about it, and any approach you rejected and why. -->

## How it was tested

<!--
`make check` is the baseline. For anything touching the agent or the protocol,
the test that counts is a real round trip: back up, change a file, back up again,
restore elsewhere, diff.
-->

- [ ] `make check` passes (gofmt, vet, tests)
- [ ] Backed up and restored real files, and the restored bytes match
- [ ] Tried it on more than one platform, or noted below which one it was tested on

## Checklist

- [ ] Comments explain *why*, not what, and none of them describe this change
- [ ] Docs updated in this change if behaviour, a flag or a default moved
- [ ] `CHANGELOG.md` updated under `## Unreleased` if a user would notice
- [ ] No new dependency, or the reason for it is in the description
- [ ] Nothing here reads or writes outside the user's own data

## Anything a reviewer should look at closely

<!--
Failure paths are where backup software lives: interrupted uploads, a full disk,
a locked file, a clock jump. Say where you are unsure.
-->
