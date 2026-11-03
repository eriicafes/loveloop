// workflow-web serves the Workflow SDK web UI at /_workflow. It reads runs
// from the World's RPC endpoint through go-workflow/world-http, configured
// by WORKFLOW_TARGET_WORLD, WORKFLOW_HTTP_BASE_URL and WORKFLOW_HTTP_TOKEN.
// The UI has no sign in of its own, so it answers only browsers signed in to
// the setup page.
import { createHash, createHmac, timingSafeEqual } from "node:crypto";
import { createWorkflowWebHandler } from "@workflow/web/handler";

const handler = await createWorkflowWebHandler({ basename: "/_workflow" });

export default {
	async fetch(request: Request): Promise<Response> {
		if (!signedIn(request)) {
			return Response.redirect(new URL("/login", request.url), 303);
		}
		return handler(request);
	},
};

// signedIn verifies the setup page's session cookie as web.App does: httpx
// signs it as base64url(HMAC-SHA256(key, json) || json), with the key
// derived from SETUP_PASSWORD in web's cookies.
function signedIn(request: Request): boolean {
	const password = process.env.SETUP_PASSWORD;
	const value = cookie(request, "loveloop_session");
	if (!password || !value) return false;
	const decoded = Buffer.from(value, "base64url");
	if (decoded.length < 32) return false;
	const signature = decoded.subarray(0, 32);
	const json = decoded.subarray(32);
	const key = createHash("sha256").update(`loveloop session ${password}`).digest();
	if (!timingSafeEqual(signature, createHmac("sha256", key).update(json).digest())) return false;
	try {
		const session: { expiresAt?: string } = JSON.parse(json.toString());
		return Date.now() < Date.parse(session.expiresAt ?? "");
	} catch {
		return false;
	}
}

function cookie(request: Request, name: string): string | undefined {
	for (const part of (request.headers.get("cookie") ?? "").split(";")) {
		const [key, ...value] = part.trim().split("=");
		if (key === name) return decodeURIComponent(value.join("="));
	}
}
