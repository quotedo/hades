defmodule Demo.MixProject do
  use Mix.Project

  def project do
    [
      app: :demo,
      version: "0.1.0",
      deps: deps(),
      aliases: aliases()
    ]
  end

  defp deps do
    [
      {:phoenix, "~> 1.8"},
      {:ecto_sql, "~> 3.12"}
    ]
  end

  defp aliases do
    [
      setup: ["deps.get", "ecto.setup"]
    ]
  end
end
