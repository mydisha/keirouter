package connectors

import "github.com/mydisha/keirouter/backend/internal/core"

// awsCredentialFields are the connect-form inputs shared by AWS-signed
// providers. withAPIKey adds the Bedrock API key (bearer) mode.
func awsCredentialFields(withAPIKey bool) []CredentialField {
	authOpts := opts(awsAuthAccessKey, "IAM access key", awsAuthEnvironment, "Server environment / instance role")
	def := awsAuthAccessKey
	if withAPIKey {
		authOpts = append(opts(awsAuthAPIKey, "Bedrock API key"), authOpts...)
		def = awsAuthAPIKey
	}
	keyed := when(awsFieldAuth, awsAuthAccessKey)
	roled := when(awsFieldAuth, awsAuthAccessKey, awsAuthEnvironment)
	return []CredentialField{
		{Key: awsFieldAuth, Label: "Authentication", Options: authOpts, Default: def, Required: true},
		{Key: awsFieldRegion, Label: "Region", Placeholder: "us-east-1", Default: "us-east-1", Required: true},
		{Key: awsFieldAccessKeyID, Label: "Access key ID", Placeholder: "AKIA…", Required: true, When: keyed},
		{Key: awsFieldSecretKey, Label: "Secret access key", Secret: true, Required: true, When: keyed},
		{Key: awsFieldSessionToken, Label: "Session token", Secret: true, Advanced: true, When: keyed,
			Hint: "Only for temporary (STS) credentials"},
		{Key: awsFieldRoleARN, Label: "Assume role ARN", Placeholder: "arn:aws:iam::123456789012:role/…", Advanced: true, When: roled,
			Hint: "Optional role to assume via STS before each session"},
		{Key: awsFieldExternalID, Label: "External ID", Advanced: true, When: roled},
	}
}

// entraFields add Microsoft Entra ID (service principal) authentication to
// the Azure providers as an alternative to the resource API key.
func entraFields() []CredentialField {
	entra := when("azure_auth", "entra")
	return []CredentialField{
		{Key: "azure_auth", Label: "Authentication", Options: opts("api_key", "API key", "entra", "Microsoft Entra ID (service principal)"),
			Default: "api_key", Advanced: true},
		{Key: "azure_tenant_id", Label: "Tenant ID", Required: true, When: entra, Advanced: true},
		{Key: "azure_client_id", Label: "Client ID", Required: true, When: entra, Advanced: true},
		{Key: "azure_client_secret", Label: "Client secret", Secret: true, Required: true, When: entra, Advanced: true},
	}
}

// cloudProviders are enterprise clouds whose authentication goes beyond a
// static bearer key: request signing, token exchange or service principals.
func cloudProviders() []ProviderSpec {
	return []ProviderSpec{
		{ID: "bedrock", DisplayName: "AWS Bedrock", Alias: "bedrock", Dialect: core.DialectBedrock,
			BaseURL: "https://bedrock-runtime.{aws_region}.amazonaws.com", AuthKind: "api_key",
			ServiceKinds: llm(core.ServiceEmbedding), Color: "#FF9900", Website: "https://aws.amazon.com/bedrock",
			APIKeyURL:   "https://console.aws.amazon.com/bedrock/home#/api-keys",
			APIKeyLabel: "Bedrock API key", APIKeyPlaceholder: "ABSK…", APIKeyWhen: when(awsFieldAuth, awsAuthAPIKey),
			Fields: append(awsCredentialFields(true),
				CredentialField{Key: "bedrock_guardrail_id", Label: "Guardrail ID", Advanced: true,
					Hint: "Applies a Bedrock guardrail to every request"},
				CredentialField{Key: "bedrock_guardrail_version", Label: "Guardrail version", Advanced: true, Placeholder: "DRAFT"}),
			Notice: "Use Bedrock model or inference-profile ids, e.g. us.anthropic.claude-sonnet-4-5-20250929-v1:0."},
		{ID: "sagemaker", DisplayName: "AWS SageMaker", Alias: "sagemaker", Dialect: core.DialectSageMaker,
			BaseURL: "https://runtime.sagemaker.{aws_region}.amazonaws.com", AuthKind: "api_key", APIKeyMode: APIKeyHidden,
			ServiceKinds: llm(), Color: "#FF9900", Website: "https://aws.amazon.com/sagemaker",
			Fields: append(awsCredentialFields(false),
				CredentialField{Key: "sagemaker_inference_component", Label: "Inference component", Advanced: true,
					Hint: "For endpoints that host several models as inference components"},
				CredentialField{Key: "sagemaker_model_name", Label: "Served model name", Advanced: true,
					Hint: "Sent as the request's model for containers that check it (vLLM, SGLang); defaults to the endpoint name"}),
			Notice: "The model is your SageMaker endpoint name. The endpoint must serve the Chat Completions message format (TGI, LMI, vLLM, SGLang)."},
		{ID: "watsonx", DisplayName: "IBM watsonx.ai", Alias: "watsonx", Dialect: core.DialectWatsonx,
			BaseURL: "https://us-south.ml.cloud.ibm.com", AuthKind: "api_key", ServiceKinds: llm(),
			Color: "#0F62FE", Website: "https://www.ibm.com/products/watsonx-ai", APIKeyURL: "https://cloud.ibm.com/iam/apikeys",
			APIKeyLabel: "API key", APIKeyPlaceholder: "IBM Cloud API key",
			Fields: []CredentialField{
				{Key: "watsonx_region", Label: "Region", Default: "us-south", Required: true, Options: opts(
					"us-south", "Dallas", "eu-de", "Frankfurt", "eu-gb", "London", "jp-tok", "Tokyo",
					"au-syd", "Sydney", "ca-tor", "Toronto", "ap-south-1", "Mumbai", "custom", "Custom URL (Cloud Pak for Data)")},
				{Key: "watsonx_url", Label: "watsonx URL", URL: true, Required: true, Placeholder: "https://cpd-…/", When: when("watsonx_region", "custom")},
				{Key: "watsonx_project_id", Label: "Project ID", Placeholder: "00000000-0000-…",
					Hint: "Required unless a deployment space ID is set"},
				{Key: "watsonx_space_id", Label: "Deployment space ID", Advanced: true},
				{Key: "watsonx_auth", Label: "Authentication", Advanced: true, Default: "iam", Options: opts(
					"iam", "IBM Cloud IAM API key", "cp4d", "Cloud Pak for Data (username + API key)", "bearer", "Bearer token (used as-is)")},
				{Key: "watsonx_username", Label: "Username", Required: true, Advanced: true, When: when("watsonx_auth", "cp4d")},
				{Key: "watsonx_api_version", Label: "API version", Default: "2024-03-13", Advanced: true},
			}},
		{ID: "oci", DisplayName: "Oracle Cloud Generative AI", Alias: "oci", Dialect: core.DialectOCI,
			BaseURL: "https://inference.generativeai.{oci_region}.oci.oraclecloud.com", AuthKind: "api_key", APIKeyMode: APIKeyHidden,
			ServiceKinds: llm(), Color: "#C74634", Website: "https://www.oracle.com/artificial-intelligence/generative-ai/",
			Fields: []CredentialField{
				{Key: "oci_region", Label: "Region", Placeholder: "us-chicago-1", Default: "us-chicago-1", Required: true},
				{Key: "oci_tenancy", Label: "Tenancy OCID", Placeholder: "ocid1.tenancy.oc1..", Required: true},
				{Key: "oci_user", Label: "User OCID", Placeholder: "ocid1.user.oc1..", Required: true},
				{Key: "oci_fingerprint", Label: "API key fingerprint", Placeholder: "12:34:56:…", Required: true},
				{Key: "oci_key", Label: "API private key (PEM)", Secret: true, Multiline: true, Required: true,
					Placeholder: "-----BEGIN PRIVATE KEY-----"},
				{Key: "oci_compartment_id", Label: "Compartment OCID", Placeholder: "ocid1.compartment.oc1..", Required: true},
				{Key: "oci_serving_mode", Label: "Serving mode", Advanced: true, Default: "ON_DEMAND",
					Options: opts("ON_DEMAND", "On-demand", "DEDICATED", "Dedicated AI cluster (model = endpoint OCID)")},
			}},
		{ID: "sap", DisplayName: "SAP Generative AI Hub", Alias: "sap", Dialect: core.DialectSAP,
			BaseURL: "https://api.ai.prod.eu-central-1.aws.ml.hana.ondemand.com", AuthKind: "api_key", APIKeyMode: APIKeyHidden,
			ServiceKinds: llm(), Color: "#0A6ED1", Website: "https://www.sap.com/products/artificial-intelligence/ai-core.html",
			Fields: []CredentialField{
				{Key: "sap_service_key", Label: "Service key (JSON)", Secret: true, Multiline: true, Placeholder: `{"clientid": …}`,
					Hint: "Paste the AI Core service key, or fill in the client fields below"},
				{Key: "sap_client_id", Label: "Client ID"},
				{Key: "sap_client_secret", Label: "Client secret", Secret: true},
				{Key: "sap_auth_url", Label: "Auth URL", URL: true, Placeholder: "https://<subdomain>.authentication.<region>.hana.ondemand.com"},
				{Key: "sap_api_url", Label: "AI API URL", URL: true, Placeholder: "https://api.ai.prod.<region>.ml.hana.ondemand.com"},
				{Key: "sap_resource_group", Label: "Resource group", Default: "default"},
				{Key: "sap_deployment_url", Label: "Orchestration deployment URL", URL: true, Advanced: true,
					Hint: "Discovered automatically when empty"},
			}},
		{ID: "azure-ai", DisplayName: "Azure AI Foundry", Alias: "azure-ai", Dialect: core.DialectOpenAI,
			BaseURL: "", AuthKind: "api_key", ServiceKinds: llm(core.ServiceEmbedding),
			Color: "#0078D4", Website: "https://ai.azure.com", APIKeyWhen: when("azure_auth", "api_key"),
			BaseURLPlaceholder: "https://<resource>.services.ai.azure.com/models",
			Fields: append(entraFields(),
				CredentialField{Key: "azure_ai_api_version", Label: "API version", Default: "2024-05-01-preview", Advanced: true}),
			Notice: "Serverless and managed model deployments from the Azure AI model catalog."},
		{ID: "databricks", DisplayName: "Databricks", Alias: "databricks", Dialect: core.DialectOpenAI,
			BaseURL: "{databricks_host}/serving-endpoints", AuthKind: "api_key", ServiceKinds: llm(core.ServiceEmbedding),
			Color: "#FF3621", Website: "https://www.databricks.com/product/model-serving",
			APIKeyLabel: "Personal access token", APIKeyPlaceholder: "dapi…", APIKeyWhen: when("databricks_auth", "pat"),
			Fields: []CredentialField{
				{Key: "databricks_host", Label: "Workspace URL", URL: true, Required: true,
					Placeholder: "https://adb-1234567890123456.7.azuredatabricks.net"},
				{Key: "databricks_auth", Label: "Authentication", Default: "pat",
					Options: opts("pat", "Personal access token", "oauth", "OAuth machine-to-machine (service principal)")},
				{Key: "databricks_client_id", Label: "Client ID", Required: true, When: when("databricks_auth", "oauth")},
				{Key: "databricks_client_secret", Label: "Client secret", Secret: true, Required: true, When: when("databricks_auth", "oauth")},
			},
			Notice: "The model is the serving endpoint name, e.g. databricks-claude-sonnet-4-5."},
		{ID: "snowflake", DisplayName: "Snowflake Cortex", Alias: "snowflake", Dialect: core.DialectOpenAI,
			BaseURL: "https://{snowflake_account}.snowflakecomputing.com/api/v2/cortex/v1", AuthKind: "api_key",
			ServiceKinds: llm(), Color: "#29B5E8", Website: "https://www.snowflake.com/en/product/features/cortex/",
			APIKeyLabel: "Programmatic access token", APIKeyWhen: when("snowflake_auth", "pat"),
			Fields: []CredentialField{
				{Key: "snowflake_account", Label: "Account identifier", Placeholder: "myorg-myaccount", Required: true},
				{Key: "snowflake_auth", Label: "Authentication", Default: "pat",
					Options: opts("pat", "Programmatic access token", "keypair", "Key-pair (JWT)")},
				{Key: "snowflake_user", Label: "User", Required: true, When: when("snowflake_auth", "keypair")},
				{Key: "snowflake_private_key", Label: "Private key (PEM, unencrypted)", Secret: true, Multiline: true, Required: true,
					When: when("snowflake_auth", "keypair"), Placeholder: "-----BEGIN PRIVATE KEY-----"},
			}},
		{ID: "replicate", DisplayName: "Replicate", Alias: "replicate", Dialect: core.DialectReplicate,
			BaseURL: "https://api.replicate.com/v1", AuthKind: "api_key", ServiceKinds: llm(),
			Color: "#3D3D3D", Website: "https://replicate.com", APIKeyURL: "https://replicate.com/account/api-tokens",
			APIKeyPlaceholder: "r8_…",
			Notice:            "Model ids are owner/name or owner/name:version, e.g. meta/meta-llama-3-70b-instruct."},
		{ID: "gigachat", DisplayName: "GigaChat", Alias: "gigachat", Dialect: core.DialectGigaChat,
			BaseURL: "https://gigachat.devices.sberbank.ru/api/v1", AuthKind: "api_key", ServiceKinds: llm(core.ServiceEmbedding),
			Color: "#21A038", Website: "https://developers.sber.ru/portal/products/gigachat-api",
			APIKeyLabel: "Authorization key", APIKeyPlaceholder: "Base64 client_id:client_secret",
			Fields: []CredentialField{
				{Key: "gigachat_scope", Label: "Scope", Default: "GIGACHAT_API_PERS", Options: opts(
					"GIGACHAT_API_PERS", "Personal", "GIGACHAT_API_B2B", "Business (prepaid)", "GIGACHAT_API_CORP", "Corporate (postpaid)")},
				{Key: "gigachat_auth_url", Label: "OAuth URL", URL: true, Advanced: true,
					Default: "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"},
			},
			Notice: "The server must trust the Russian Trusted Root CA used by the GigaChat endpoints."},
		{ID: "huggingface-router", DisplayName: "Hugging Face Inference Providers", Alias: "hf-router", Dialect: core.DialectOpenAI,
			BaseURL: "https://router.huggingface.co/v1", AuthKind: "api_key", ServiceKinds: llm(),
			Color: "#FFD21E", Website: "https://huggingface.co/docs/inference-providers", APIKeyURL: "https://huggingface.co/settings/tokens",
			APIKeyPlaceholder: "hf_…"},
	}
}

// compat is shorthand for an API-key provider speaking Chat Completions.
func compat(id, name, base, color, website, keyURL string) ProviderSpec {
	return ProviderSpec{ID: id, DisplayName: name, Alias: id, Dialect: core.DialectOpenAI, BaseURL: base,
		AuthKind: "api_key", ServiceKinds: llm(), Color: color, Website: website, APIKeyURL: keyURL}
}

// local is shorthand for a self-hosted Chat Completions server that usually
// runs without a key.
func local(id, name, base, color, website string) ProviderSpec {
	return ProviderSpec{ID: id, DisplayName: name, Alias: id, Dialect: core.DialectOpenAI, BaseURL: base,
		AuthKind: "none", AuthModes: []string{"none", "api_key"}, ServiceKinds: llm(), Color: color, Website: website,
		Notice: "Self-hosted server. Point the base URL at your instance; requires KEIROUTER_SECURITY__ALLOW_PRIVATE_BASE_URL=true for loopback or LAN hosts."}
}

// compatProviders are hosted and self-hosted endpoints that speak the Chat
// Completions API with a bearer key (or no key).
func compatProviders() []ProviderSpec {
	withRegions := func(p ProviderSpec, def string, regions ...RegionOption) ProviderSpec {
		p.Regions, p.DefaultRegion = regions, def
		return p
	}
	notice := func(p ProviderSpec, n string) ProviderSpec { p.Notice = n; return p }
	kinds := func(p ProviderSpec, k ...core.ServiceKind) ProviderSpec { p.ServiceKinds = llm(k...); return p }

	return []ProviderSpec{
		// Model labs and first-party platforms.
		compat("codestral", "Codestral", "https://codestral.mistral.ai/v1", "#FA520F", "https://mistral.ai/news/codestral", "https://console.mistral.ai/codestral"),
		withRegions(compat("dashscope", "Alibaba Model Studio", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "#FF6A00",
			"https://www.alibabacloud.com/product/modelstudio", "https://modelstudio.console.alibabacloud.com/?tab=playground#/api-key"), "intl",
			RegionOption{ID: "intl", Label: "International (Singapore)", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
			RegionOption{ID: "us", Label: "US (Virginia)", BaseURL: "https://dashscope-us.aliyuncs.com/compatible-mode/v1"},
			RegionOption{ID: "cn", Label: "China (Beijing)", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"}),
		withRegions(compat("moonshot", "Moonshot AI", "https://api.moonshot.ai/v1", "#16191E", "https://platform.moonshot.ai", "https://platform.moonshot.ai/console/api-keys"), "global",
			RegionOption{ID: "global", Label: "Global", BaseURL: "https://api.moonshot.ai/v1"},
			RegionOption{ID: "cn", Label: "China", BaseURL: "https://api.moonshot.cn/v1"}),
		compat("zai", "Z.AI", "https://api.z.ai/api/paas/v4", "#2563EB", "https://z.ai", "https://z.ai/manage-apikey/apikey-list"),
		compat("modelscope", "ModelScope", "https://api-inference.modelscope.cn/v1", "#624AFF", "https://modelscope.cn", "https://modelscope.cn/my/myaccesstoken"),
		compat("meta-llama", "Llama API", "https://api.llama.com/compat/v1", "#0467DF", "https://llama.developer.meta.com", "https://llama.developer.meta.com/api-keys"),
		compat("meta", "Meta Model API", "https://api.meta.ai/v1", "#0467DF", "https://ai.meta.com", ""),
		compat("amazon-nova", "Amazon Nova", "https://api.nova.amazon.com/v1", "#FF9900", "https://nova.amazon.com", "https://nova.amazon.com/dev/api"),
		compat("inception", "Inception Labs", "https://api.inceptionlabs.ai/v1", "#111827", "https://www.inceptionlabs.ai", "https://platform.inceptionlabs.ai"),
		compat("v0", "v0", "https://api.v0.dev/v1", "#000000", "https://v0.dev", "https://v0.dev/chat/settings/keys"),
		compat("tencent", "Tencent TokenHub", "https://tokenhub-intl.tencentcloudmaas.com/v1", "#006EFF", "https://www.tencentcloud.com/products/hunyuan", ""),
		compat("maritalk", "Maritaca AI", "https://chat.maritaca.ai/api", "#7C3AED", "https://www.maritaca.ai", "https://plataforma.maritaca.ai/chaves-de-api"),
		compat("sarvam", "Sarvam AI", "https://api.sarvam.ai/v1", "#FF7A00", "https://www.sarvam.ai", "https://dashboard.sarvam.ai"),
		compat("cognition", "Cognition", "https://api.cognition.ai/v1", "#111827", "https://cognition.ai", ""),

		// Inference clouds and GPU marketplaces.
		compat("lambda-ai", "Lambda", "https://api.lambda.ai/v1", "#6E56CF", "https://lambda.ai/inference", "https://cloud.lambda.ai/api-keys"),
		compat("friendliai", "FriendliAI", "https://api.friendli.ai/serverless/v1", "#2C5BFF", "https://friendli.ai", "https://friendli.ai/suite/setting/tokens"),
		compat("empower", "Empower", "https://app.empower.dev/api/v1", "#0EA5E9", "https://empower.dev", ""),
		compat("galadriel", "Galadriel", "https://api.galadriel.ai/v1", "#111827", "https://galadriel.com", ""),
		compat("heroku", "Heroku Managed Inference", "https://us.inference.heroku.com/v1", "#430098", "https://www.heroku.com/ai", ""),
		compat("ovhcloud", "OVHcloud AI Endpoints", "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1", "#123F6D", "https://endpoints.ai.cloud.ovh.net", ""),
		compat("gradient-ai", "DigitalOcean Gradient AI", "https://inference.do-ai.run/v1", "#0080FF", "https://www.digitalocean.com/products/gradientai", "https://cloud.digitalocean.com/gen-ai/model-access-keys"),
		compat("wandb", "W&B Inference", "https://api.inference.wandb.ai/v1", "#FFBE00", "https://wandb.ai/site/inference", "https://wandb.ai/authorize"),
		compat("clarifai", "Clarifai", "https://api.clarifai.com/v2/ext/openai/v1", "#1955FF", "https://clarifai.com", "https://clarifai.com/settings/security"),
		compat("compactifai", "CompactifAI", "https://api.compactif.ai/v1", "#0F766E", "https://multiversecomputing.com/compactifai", ""),
		compat("datarobot", "DataRobot", "https://app.datarobot.com/api/v2/genai/llmgw", "#6125E0", "https://www.datarobot.com", ""),
		compat("crusoe", "Crusoe Cloud", "https://managed-inference-api-proxy.crusoecloud.com/v1", "#FF6B00", "https://crusoe.ai", ""),
		compat("parasail", "Parasail", "https://api.parasail.io/v1", "#0EA5E9", "https://www.parasail.io", ""),
		compat("gmi", "GMI Cloud", "https://api.gmi-serving.com/v1", "#111827", "https://www.gmicloud.ai", ""),
		compat("tensormesh", "Tensormesh", "https://serverless.tensormesh.ai/v1", "#4F46E5", "https://tensormesh.ai", ""),
		compat("cortecs", "Cortecs", "https://api.cortecs.ai/v1", "#2563EB", "https://cortecs.ai", ""),
		compat("libertai", "LibertAI", "https://api.libertai.io/v1", "#7C3AED", "https://libertai.io", ""),
		compat("synthetic", "Synthetic", "https://api.synthetic.new/openai/v1", "#111827", "https://synthetic.new", ""),
		compat("prism", "Prism Inference", "https://api.prisminference.com/v1", "#9333EA", "https://prisminference.com", ""),
		compat("sail", "Sail Research", "https://api.sailresearch.com/v1", "#0EA5E9", "https://sailresearch.com", ""),
		compat("scx-ai", "SCX.ai", "https://api.scx.ai/v1", "#16A34A", "https://scx.ai", ""),
		compat("darkbloom", "Darkbloom", "https://api.darkbloom.dev/v1", "#1F2937", "https://darkbloom.dev", ""),
		compat("pinstripes", "Pinstripes", "https://pinstripes.io/v1", "#1E3A8A", "https://pinstripes.io", ""),
		compat("empiriolabs", "Empirio Labs", "https://api.empiriolabs.ai/v1", "#0F172A", "https://empiriolabs.ai", ""),
		compat("neosantara", "Neosantara", "https://api.neosantara.xyz/v1", "#DC2626", "https://neosantara.xyz", ""),
		compat("llamagate", "LlamaGate", "https://api.llamagate.dev/v1", "#0891B2", "https://llamagate.dev", ""),
		compat("abliteration", "Abliteration", "https://api.abliteration.ai/v1", "#374151", "https://abliteration.ai", ""),
		compat("charity-engine", "Charity Engine", "https://api.charityengine.services/remotejobs/v2/inference", "#16A34A", "https://www.charityengine.com", ""),

		// Aggregators and gateways.
		compat("helicone", "Helicone AI Gateway", "https://ai-gateway.helicone.ai", "#0EA5E9", "https://www.helicone.ai", "https://us.helicone.ai/settings/api-keys"),
		compat("cometapi", "CometAPI", "https://api.cometapi.com/v1", "#2563EB", "https://www.cometapi.com", "https://api.cometapi.com/console/token"),
		compat("aihubmix", "AIHubMix", "https://aihubmix.com/v1", "#6366F1", "https://aihubmix.com", ""),
		compat("edenai", "Eden AI", "https://api.edenai.run/v3/llm", "#4F46E5", "https://www.edenai.co", "https://app.edenai.run/admin/iam/api-keys"),
		compat("nano-gpt", "NanoGPT", "https://nano-gpt.com/api/v1", "#111827", "https://nano-gpt.com", "https://nano-gpt.com/api"),
		compat("poe", "Poe", "https://api.poe.com/v1", "#5D5CDE", "https://poe.com", "https://poe.com/api_key"),
		compat("apertis", "Apertis", "https://api.stima.tech/v1", "#0EA5E9", "https://stima.tech", ""),
		compat("nadir", "Nadir", "https://api.getnadir.com/v1", "#111827", "https://getnadir.com", ""),
		kinds(compat("github-models", "GitHub Models", "https://models.github.ai/inference", "#24292F",
			"https://github.com/marketplace/models", "https://github.com/settings/personal-access-tokens"), core.ServiceEmbedding),

		// Self-hosted servers.
		local("lm-studio", "LM Studio", "http://localhost:1234/v1", "#4338CA", "https://lmstudio.ai"),
		local("llamafile", "llamafile", "http://localhost:8080/v1", "#F59E0B", "https://github.com/Mozilla-Ocho/llamafile"),
		local("docker-model-runner", "Docker Model Runner", "http://localhost:12434/engines/v1", "#2496ED", "https://docs.docker.com/ai/model-runner/"),
		local("xinference", "Xinference", "http://localhost:9997/v1", "#7C3AED", "https://inference.readthedocs.io"),
		local("lemonade", "Lemonade", "http://localhost:8000/api/v1", "#FACC15", "https://lemonade-server.ai"),
		local("oobabooga", "Text Generation WebUI", "http://localhost:5000/v1", "#6B7280", "https://github.com/oobabooga/text-generation-webui"),
		notice(local("triton", "NVIDIA Triton", "http://localhost:9000/v1", "#76B900", "https://github.com/triton-inference-server/server"),
			"Triton's OpenAI-compatible frontend. Point the base URL at your Triton server; requires KEIROUTER_SECURITY__ALLOW_PRIVATE_BASE_URL=true for loopback or LAN hosts."),
		ragflow(),
	}
}

// ragflow exposes each chat assistant (or agent) as its own Chat Completions
// endpoint, so the base URL is per account.
func ragflow() ProviderSpec {
	p := compat("ragflow", "RAGFlow", "", "#1D4ED8", "https://ragflow.io", "")
	p.BaseURLPlaceholder = "http://<host>:9380/api/v1/chats_openai/<chat_id>"
	p.Notice = "Use the chat assistant (chats_openai/<chat_id>) or agent (agents_openai/<agent_id>) endpoint as the base URL."
	return p
}

// Discovery hints for providers whose model list is not available (or not
// useful) through GET /models.
func init() {
	for id, models := range map[string][]ModelSpec{
		"snowflake": {
			m("claude-sonnet-4-5", "Claude Sonnet 4.5"), m("claude-4-sonnet", "Claude 4 Sonnet"), m("claude-3-7-sonnet", "Claude 3.7 Sonnet"),
			m("openai-gpt-5", "GPT-5"), m("openai-gpt-4.1", "GPT-4.1"), m("llama4-maverick", "Llama 4 Maverick"),
			m("snowflake-llama-3.3-70b", "Snowflake Llama 3.3 70B"), m("mistral-large2", "Mistral Large 2"), m("deepseek-r1", "DeepSeek R1"),
		},
		"databricks": {
			m("databricks-claude-sonnet-4-5", "Claude Sonnet 4.5"), m("databricks-gpt-oss-120b", "GPT OSS 120B"),
			m("databricks-llama-4-maverick", "Llama 4 Maverick"), m("databricks-meta-llama-3-3-70b-instruct", "Llama 3.3 70B Instruct"),
			m("databricks-gemini-2-5-pro", "Gemini 2.5 Pro"),
			emb("databricks-gte-large-en", "GTE Large (en)", 1024),
		},
		"azure-ai": {
			m("DeepSeek-R1", "DeepSeek R1"), m("Llama-4-Maverick-17B-128E-Instruct-FP8", "Llama 4 Maverick"),
			m("Phi-4", "Phi-4"), m("mistral-medium-2505", "Mistral Medium 3"), m("grok-3", "Grok 3"),
		},
		"codestral": {m("codestral-latest", "Codestral"), m("codestral-2508", "Codestral 25.08")},
		"dashscope": {m("qwen3-max", "Qwen3 Max"), m("qwen-plus", "Qwen Plus"), m("qwen-flash", "Qwen Flash"), m("qwen3-coder-plus", "Qwen3 Coder Plus"), m("qwen3-vl-plus", "Qwen3 VL Plus")},
		"moonshot":  {m("kimi-k2.6", "Kimi K2.6"), m("kimi-k2.5", "Kimi K2.5"), m("kimi-k2-thinking", "Kimi K2 Thinking"), m("kimi-latest", "Kimi Latest")},
		"zai":       {m("glm-5.1", "GLM 5.1"), m("glm-5", "GLM 5"), m("glm-4.7", "GLM-4.7"), m("glm-4.6", "GLM-4.6"), m("glm-4.5-air", "GLM-4.5-Air")},
		"github-models": {
			m("openai/gpt-5", "GPT-5"), m("openai/gpt-4.1", "GPT-4.1"), m("openai/gpt-4o", "GPT-4o"),
			m("meta/Llama-4-Maverick-17B-128E-Instruct-FP8", "Llama 4 Maverick"), m("deepseek/DeepSeek-R1", "DeepSeek R1"),
			m("mistral-ai/mistral-medium-2505", "Mistral Medium 3"), m("xai/grok-3", "Grok 3"),
		},
		"meta-llama": {m("Llama-4-Maverick-17B-128E-Instruct-FP8", "Llama 4 Maverick"), m("Llama-3.3-70B-Instruct", "Llama 3.3 70B Instruct")},
	} {
		if _, exists := providerModels[id]; !exists {
			providerModels[id] = models
		}
	}
}
