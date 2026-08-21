# The private tap's install, asserted after the private flow has run. Split
# from the public suite because the assertion cannot precede the install.
#
# see docs/formats/brew-tap.md

tests:
	- desc: the privately installed binary executes
	  cmd: myapp
	  outputs:
		stdout:
			- "buildhost-homebrew-private-ok"
