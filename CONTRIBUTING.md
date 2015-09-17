# Contributing to Delve

Want to help contribute to Delve? Great! Any and all help is certainly appreciated, whether it's code, documentation, or spelling corrections.

If you are planning to contribute a significant change, please draft a design document (or start a conversation) and post it to the [developer mailing list](https://groups.google.com/forum/#!forum/delve-dev). This will allow other developers and users to discuss the proposed change.

If you'd like to join the discussion, join the gitter chat (link in README).

## Filing issues

When filing an issue, make sure to answer these five questions:

1. What version of Delve are you using (`dlv version`)?
2. What version of Go are you using? (`go version`)?
3. What operating system and processor architecture are you using?
4. What did you do?
5. What did you expect to see?
6. What did you see instead?

## Contributing code

Fork this repo and create your own feature branch. Install all dependencies as documented in the README.

### Guidelines

Consider the following guidelines when preparing to submit a patch:

* Follow standard Go conventions (document any new exported types, funcs, etc.., ensuring proper punctuation).
* Ensure that you test your code. Any patches sent in for new / fixed functionality must include tests in order to be merged into master.
* If you plan on making any major changes, create an issue before sending a patch. This will allow for proper discussion beforehand.
* Keep any os / arch specific code contained to os / arch specific files. Delve leverages Go's filename based conditional compilation, i.e do not put Linux specific functionality in a non Linux specific file.

### Format of the Commit Message

We follow a rough convention for commit messages that is designed to answer two
questions: what changed and why. The subject line should feature the what and
the body of the commit should describe the why.

```
terminal/command: Add 'list' command

This change adds the 'list' command which will print either your current
location, or the location that you specify using the Delve linespec.

Fixes #38
```

The format can be described more formally as follows:

```
<subsystem>: <what changed>
<BLANK LINE>
<why this change was made>
<BLANK LINE>
<footer>
```

The first line is the subject and should be no longer than 70 characters, the
second line is always blank, and other lines should be wrapped at 80 characters.
This allows the message to be easier to read on GitHub as well as in various
git tools.
