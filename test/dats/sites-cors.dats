# A hosted site must stay loadable CROSS-ORIGIN, through its redirects, in a
# real browser.
#
# This exists because that stopped being true and every cross-origin consumer
# broke at once: the legacy /{project}/branch/{branch}/ URL became a 302 to the
# canonical @{branch} form, and the redirect went out without the site CORS
# headers. A browser re-checks the header on every hop, so the load died at the
# 302 and never reached the 200 that carried it. curl follows redirects without
# enforcing CORS at all and reported a clean 200 the whole time.
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
