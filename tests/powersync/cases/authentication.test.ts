import { describe, expect, test } from 'vitest';
import { loadEnvironment } from './helpers.js';

describe('PowerSync JWT authentication', () => {
	test('rejects a signed token with the wrong audience', async (context) => {
		const env = loadEnvironment();
		if (env == null || env.invalidAudienceToken === '') return context.skip();
		const response = await fetch(`${env.gizpayEndpoint}/sync/stream`, {
			method: 'POST',
			headers: { Authorization: `Bearer ${env.invalidAudienceToken}`, 'Content-Type': 'application/json' },
			body: '{}'
		});
		expect(response.status).toBe(401);
	});
});
