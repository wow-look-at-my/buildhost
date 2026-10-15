// The deployment step of buildhost-publish-docker.

import { createDockerDeployment } from '../deployment';

await createDockerDeployment({ core, github, context, octokit, exec, inputs, steps });
