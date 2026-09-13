# syntax=docker/dockerfile:1

# The apt client for test/dats/apt-install.dats.
#
# The suite used to install onto the runner itself, which made it depend on
# whatever apt state that host carried and left cleanup to a teardown. A
# container fixes both: exactly one extra source, an apt state baked at build
# time, and nothing to undo afterwards.
#
# Building it is the workflow's job, in its own untimed step, so the suite's
# setup hook never pays for a base-image pull or these installs.
FROM ubuntu:24.04

# curl and gnupg fetch and dearmor the repository key. systemd supplies the
# systemctl a create_service package's postinst calls. The apt lists stay in the
# image so a package's dependencies still resolve.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		gnupg \
		systemd

# One test asserts the APE launcher works for a user who cannot write /usr/bin,
# which is the case that was broken. Root cannot exercise it.
RUN useradd --create-home --no-log-init --uid 1001 aptuser
