// The deployment-status step of buildhost-publish-site.

import { finishSiteDeployment } from '../deployment';

await finishSiteDeployment({ core, github, context, octokit, exec, inputs, steps });
