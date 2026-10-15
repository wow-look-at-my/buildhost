// The push step of buildhost-docker-push.

import { dockerPush } from '../docker';

await dockerPush({ core, github, context, octokit, exec, inputs, steps });
