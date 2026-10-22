const crypto = require("node:crypto");
const dns = require("node:dns/promises");
const https = require("node:https");
const { Client } = require("pg");
const { Kafka, logLevel } = require("kafkajs");
const { createClient } = require("redis");
const { KMSClient, DecryptCommand } = require("@aws-sdk/client-kms");
const { S3Client, ListObjectsV2Command } = require("@aws-sdk/client-s3");

const results = [];
const config = { region: process.env.AWS_REGION, maxAttempts: 2 };
const kms = new KMSClient(config);
const s3 = new S3Client(config);
const timeout = setTimeout(() => {
  console.error("Dependency validation exceeded its time limit");
  process.exit(1);
}, 240000);

async function check(name, fn) {
  try {
    const detail = await fn();
    results.push({ name, ok: true, ...detail });
    console.log(JSON.stringify(results.at(-1)));
  } catch {
    results.push({ name, ok: false });
    console.error(JSON.stringify(results.at(-1)));
  }
}

function verifyCipher(key, encoded, encoding) {
  const raw = Buffer.from(encoded, encoding);
  if (raw.length < 28) throw new Error("Invalid ciphertext");
  const decipher = crypto.createDecipheriv("aes-256-gcm", key, raw.subarray(0, 12));
  decipher.setAuthTag(raw.subarray(raw.length - 16));
  const first = decipher.update(raw.subarray(12, raw.length - 16));
  const last = decipher.final();
  first.fill(0);
  last.fill(0);
}

async function withDatabase(connectionString, fn) {
  const client = new Client({ connectionString, connectionTimeoutMillis: 10000, query_timeout: 20000 });
  client.on("error", () => {});
  try {
    await client.connect();
    await client.query("BEGIN READ ONLY");
    await client.query("SET LOCAL statement_timeout = '15s'");
    return await fn(client);
  } finally {
    await client.end().catch(() => {});
  }
}

function requestJSON(url, headers = {}) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers, timeout: 15000 }, (res) => {
      let body = "";
      res.on("data", (chunk) => { body += chunk; });
      res.on("end", () => {
        if (res.statusCode !== 200) return reject(new Error("Unexpected response"));
        try { resolve(JSON.parse(body)); } catch { reject(new Error("Unexpected JSON")); }
      });
    });
    req.on("timeout", () => req.destroy());
    req.on("error", reject);
  });
}

async function main() {
  await check("planetscale_private_dns", async () => {
    const host = new URL(process.env.PRIMARY_DB).hostname;
    const addresses = await dns.resolve4(host);
    if (!addresses.length || addresses.some((ip) => !ip.startsWith("10."))) throw new Error("Private DNS required");
    return { private_addresses: addresses.length };
  });

  for (const [name, variable] of [["backend_database", "PRIMARY_DB"], ["consumer_database", "CONSUMER_DB"], ["realtime_database", "REALTIME_DB"]]) {
    await check(name, () => withDatabase(process.env[variable], async (db) => {
      const out = await db.query("SELECT count(*)::int AS readable_tables FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r' AND has_table_privilege(c.oid,'SELECT')");
      const version = await db.query("SELECT version,dirty FROM schema_migrations");
      if (!out.rows[0].readable_tables || version.rows.some((r) => r.dirty)) throw new Error("Database validation failed");
      return { readable_tables: out.rows[0].readable_tables, migration_clean: true };
    }));
  }

  await check("stored_credentials_and_kms", () => withDatabase(process.env.PRIMARY_DB, async (db) => {
    const key = Buffer.from(process.env.CREDENTIALS_ENCRYPTION_KEY, "hex");
    const fields = await db.query("SELECT v AS value FROM email_accounts_smtp_imap CROSS JOIN LATERAL (VALUES (smtp_host),(smtp_user),(smtp_password),(imap_host),(imap_user),(imap_password)) fields(v) WHERE v IS NOT NULL AND v<>'' UNION ALL SELECT v FROM email_accounts_oauth CROSS JOIN LATERAL (VALUES(access_token),(refresh_token)) fields(v) WHERE v IS NOT NULL AND v<>'' UNION ALL SELECT secret FROM webhook_endpoints WHERE secret IS NOT NULL AND secret<>''");
    for (const row of fields.rows) verifyCipher(key, row.value, "hex");
    key.fill(0);
    const mfaKey = crypto.createHash("sha256").update(process.env.AUTH_SECRET).digest();
    const mfa = await db.query("SELECT totp_secret_sealed FROM user_totp_settings WHERE totp_enabled");
    for (const row of mfa.rows) verifyCipher(mfaKey, row.totp_secret_sealed, "base64");
    mfaKey.fill(0);
    const wrapped = await db.query("SELECT encrypted_data_key FROM organization_encrypted_keys LIMIT 1");
    if (!wrapped.rowCount) throw new Error("No data key available to verify");
    const out = await kms.send(new DecryptCommand({ CiphertextBlob: Buffer.from(wrapped.rows[0].encrypted_data_key, "base64") }));
    if (out.Plaintext?.length !== 32) throw new Error("Invalid data key");
    out.Plaintext.fill(0);
    return { credential_fields: fields.rowCount, mfa_enrollments: mfa.rowCount, kms_decrypt_as_task_role: true };
  }));

  await check("existing_shared_cache", async () => {
    const client = createClient({ url: process.env.REDIS, socket: { connectTimeout: 10000, reconnectStrategy: false } });
    client.on("error", () => {});
    try { await client.connect(); if (await client.ping() !== "PONG") throw new Error("Cache check failed"); }
    finally { client.destroy(); }
    return {};
  });
  await check("private_valkey", async () => {
    const client = createClient({ password: process.env.NEW_REDIS_PASSWORD, socket: { host: process.env.CACHE_HOST, port: 6379, tls: true, servername: process.env.CACHE_HOST, rejectUnauthorized: true, connectTimeout: 10000, reconnectStrategy: false } });
    client.on("error", () => {});
    try { await client.connect(); if (await client.ping() !== "PONG") throw new Error("Cache check failed"); }
    finally { client.destroy(); }
    return { tls_verified: true };
  });
  await check("kafka_replacement_key", async () => {
    const kafka = new Kafka({ clientId: "aws-migration-validation", brokers: process.env.KAFKA_BOOTSTRAP_SERVERS.split(","), ssl: true, sasl: { mechanism: "plain", username: process.env.KAFKA_SASL_USERNAME, password: process.env.KAFKA_SASL_PASSWORD }, logLevel: logLevel.NOTHING, connectionTimeout: 10000, requestTimeout: 15000, retry: { retries: 1 } });
    const admin = kafka.admin({ allowAutoTopicCreation: false });
    try { await admin.connect(); const out = await admin.fetchTopicMetadata({ topics: ["tracking-events"] }); return { existing_topics: out.topics.length }; }
    finally { await admin.disconnect(); }
  });
  await check("schema_registry_replacement_key", async () => {
    const authorization = "Basic " + Buffer.from(process.env.SCHEMA_REGISTRY_KEY + ":" + process.env.SCHEMA_REGISTRY_SECRET).toString("base64");
    const subjects = await requestJSON(process.env.SCHEMA_REGISTRY_URL.replace(/\/$/, "") + "/subjects", { authorization });
    if (!Array.isArray(subjects)) throw new Error("Invalid Schema Registry response");
    return { subjects: subjects.length };
  });
  await check("blob_bucket_as_task_role", async () => {
    await s3.send(new ListObjectsV2Command({ Bucket: process.env.BLOB_BUCKET, MaxKeys: 1 }));
    return {};
  });
  kms.destroy();
  s3.destroy();
  clearTimeout(timeout);
  console.log(JSON.stringify({ checks: results.length, passed: results.filter((r) => r.ok).length, production_writes: false }));
  process.exit(results.every((r) => r.ok) ? 0 : 1);
}

main().catch(() => { console.error("Dependency validation failed; credential values omitted"); process.exit(1); });
