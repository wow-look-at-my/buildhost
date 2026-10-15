// The deployment step of buildhost-publish-site.

import { createSiteDeployment } from '../deployment';

await createSiteDeployment({ core, github, context, octokit, exec, inputs, steps });
