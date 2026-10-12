# A hosted site must stay loadable CROSS-ORIGIN, through its redirects, in a
# real browser.
#
# Driving a browser and inspecting each hop is a program, so it lives in a node
# test the suite invokes. The workflow installs playwright-core and maps
# sites.localhost; $BUILDHOST_BIN names the server binary.
#
# see docs/sites.md
tests:
	- desc: every redirect hop carries CORS and a browser imports the module across origins
	  cmd: node test/actions/sites-cors.test.ts
	  outputs:
		stdout:
			- "real browser imported the module cross-origin through the redirect"
			- "hosted sites are loadable cross-origin, redirects included"
