import { GlobalRegistrator } from "@happy-dom/global-registrator";

// happy-dom's fetch and Response would replace Bun's, which the server's
// tests use, so only the document and the window's other parts are added.
const { fetch, Request, Response, Headers } = globalThis;
GlobalRegistrator.register();
Object.assign(globalThis, { fetch, Request, Response, Headers });
