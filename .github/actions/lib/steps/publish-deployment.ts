// The deployment step of buildhost-publish.

import { createPublishDeployment } from '../deployment';

await createPublishDeployment({ core, github, context, octokit, exec, inputs, steps });
