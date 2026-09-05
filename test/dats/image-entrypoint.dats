# The SHIPPED IMAGE must start however its entrypoint names the binary.
#
# This exists because the deployment sat on a version that could not start for
# two weeks while CI stayed green. The image entered through ["/bin/sh", <APE>],
# so it started only when something spelled the entrypoint that exact way. The
# rolling updater creates the new container from the OLD container's config,
# which carries the resolved entrypoint of the image it was created from -- for
# a container predating the APE, ["buildhost"]. That is a bare exec of an APE,
# which the kernel answers with ENOEXEC and docker reports as exit 126.
#
# Compose starts the container the one way that always worked, so no test here
# could see it. These start the image the OTHER ways instead.
#
# Needs the docker CLI and the buildhost:ci image the compose build tags, so a
# workflow runs it --no-sandbox.

tests:
	- desc: the entrypoint on PATH is a shebang script, not the APE itself
	  cmd: docker run --rm --entrypoint /bin/sh buildhost:ci -c 'head -n1 /usr/local/bin/buildhost'
	  outputs:
		stdout:
			- "#!/bin/sh"

	# The failing spelling, verbatim: the name alone, resolved through PATH.
	- desc: a bare exec of the binary by name runs it
	  cmd: docker run --rm --entrypoint buildhost buildhost:ci version
	  outputs:
		stdout:
			- "buildhost"

	- desc: a bare exec by absolute path runs it too
	  cmd: docker run --rm --entrypoint /usr/local/bin/buildhost buildhost:ci version
	  outputs:
		stdout:
			- "buildhost"

	- desc: the image starts through its own entrypoint
	  cmd: docker run --rm buildhost:ci version
	  outputs:
		stdout:
			- "buildhost"

	# The spelling the image used until now, which an older container config
	# still carries. Passing sh a shebang script is the same as running it.
	- desc: a shell in front of the launcher still works
	  cmd: docker run --rm --entrypoint /bin/sh buildhost:ci /usr/local/bin/buildhost version
	  outputs:
		stdout:
			- "buildhost"
