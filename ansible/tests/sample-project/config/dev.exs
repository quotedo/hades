import Config

config :sample, Sample.Repo, url: System.fetch_env!("DATABASE_URL")

config :sample, SampleWeb.Endpoint,
  http: [ip: {127, 0, 0, 1}, port: String.to_integer(System.fetch_env!("PORT"))],
  url: [host: System.fetch_env!("PHX_HOST"), scheme: "https", port: 443]
