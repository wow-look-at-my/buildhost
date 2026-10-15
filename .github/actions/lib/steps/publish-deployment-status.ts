// The deployment-status step of buildhost-publish.

import { finishPublishDeployment } from '../deployment';

await finishPublishDeployment({ core, github, context, octokit, exec, inputs, steps });
