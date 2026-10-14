# Run from realtime/: MIX_ENV=test mix run --no-start bench/repeated_work.exs
defmodule RepeatedWorkBench do
  alias Realtime.Connections
  alias Realtime.EventType

  def run do
    IO.puts("Synthetic local benchmark, not production CPU evidence")

    IO.puts(
      "Elixir #{System.version()}, OTP #{System.otp_release()}, schedulers #{System.schedulers_online()}"
    )

    IO.puts("Five alternating before/after rounds; medians, microseconds and process reductions")

    :ets.new(:realtime_connections, [:named_table, :set, :protected])
    :ets.new(:realtime_connection_total, [:named_table, :set, :protected, read_concurrency: true])

    for users <- [100, 1_000, 10_000] do
      :ets.delete_all_objects(:realtime_connections)
      :ets.insert(:realtime_connections, for(id <- 1..users, do: {"user-#{id}", 2}))
      :ets.insert(:realtime_connection_total, {:total, users * 2})

      before = fn ->
        :ets.foldl(fn {_, count}, acc -> acc + count end, 0, :realtime_connections)
      end

      compare(
        "connection total, #{users} users, 2,000 reads",
        2_000,
        before,
        &Connections.total_local/0
      )
    end

    for type <- ["CAMPAIGN_PROGRESS", "campaign.progress", "évent:créé"] do
      compare(
        "normalize #{inspect(type)}, 100,000 events",
        100_000,
        fn -> original(type) end,
        fn -> EventType.normalize(type) end
      )

      compare(
        "permission+intent normalization #{inspect(type)}, 100,000 events",
        100_000,
        fn -> {original(type), original(type)} end,
        fn ->
          normalized = EventType.normalize(type)
          {normalized, normalized}
        end
      )
    end
  end

  defp original(value),
    do: value |> to_string() |> String.upcase() |> String.replace(~r/[.:\s-]+/, "_")

  defp compare(label, iterations, before, after_fun) do
    if before.() != after_fun.(), do: raise("different result: #{label}")
    repeat(before, 100)
    repeat(after_fun, 100)

    rounds =
      for round <- 1..5 do
        if rem(round, 2) == 1 do
          {measure(before, iterations), measure(after_fun, iterations)}
        else
          after_result = measure(after_fun, iterations)
          {measure(before, iterations), after_result}
        end
      end

    {before_us, before_reductions} = medians(Enum.map(rounds, &elem(&1, 0)))
    {after_us, after_reductions} = medians(Enum.map(rounds, &elem(&1, 1)))

    IO.puts(
      "#{label}: before=#{before_us}us/#{before_reductions} reductions after=#{after_us}us/#{after_reductions} reductions"
    )
  end

  defp measure(fun, iterations) do
    task =
      Task.async(fn ->
        {:reductions, start} = Process.info(self(), :reductions)
        {us, _} = :timer.tc(fn -> repeat(fun, iterations) end)
        {:reductions, finish} = Process.info(self(), :reductions)
        {us, finish - start}
      end)

    Task.await(task, :infinity)
  end

  defp medians(results) do
    {results |> Enum.map(&elem(&1, 0)) |> Enum.sort() |> Enum.at(2),
     results |> Enum.map(&elem(&1, 1)) |> Enum.sort() |> Enum.at(2)}
  end

  defp repeat(_fun, 0), do: :ok

  defp repeat(fun, n) do
    fun.()
    repeat(fun, n - 1)
  end
end

RepeatedWorkBench.run()
