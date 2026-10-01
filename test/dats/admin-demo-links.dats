# Every in-app link on the PREVIEW dashboard must reach a page that renders.
#
# The preview publishes the admin SPA with no backend, so every page draws from
# the built-in demo dataset. That dataset once linked to pages it had no data
# for: clicking a release threw inside the renderer, left the previous page on
# screen, and read as a link that does nothing. Only walking the links the app
# itself renders finds that, so the crawl drives a real browser and lives in a
# node test the suite invokes.
#
# The workflow builds internal/admin/static and installs playwright-core;
# $PLAYWRIGHT_CHROME_PATH names a browser when there is one outside a runner.
#
# see docs/admin-frontend.md

tests:
	- desc: every link the preview dashboard renders reaches a page that draws
	  cmd: node test/actions/admin-demo-links.test.ts
	  outputs:
		stdout:
			- "every in-app link rendered"
		!stdout:
			- "rendered the error page"
