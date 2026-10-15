// The deployment-status step of buildhost-publish-docker.

import { finishDockerDeployment } from '../deployment';

await finishDockerDeployment({ core, github, context, octokit, exec, inputs, steps });
