defmodule Realtime.ConnectionsTest do
  use ExUnit.Case, async: false

  alias Realtime.Connections

  setup do
    restart_tracker()

    old_limits =
      for key <- [:max_connections_per_user, :max_connections_per_ip, :max_connections_global],
          do: {key, Application.get_env(:realtime, key)}

    Application.put_env(:realtime, :max_connections_per_user, 2)
    Application.put_env(:realtime, :max_connections_per_ip, 3)
    Application.put_env(:realtime, :max_connections_global, 4)

    on_exit(fn ->
      Enum.each(old_limits, fn {key, value} -> Application.put_env(:realtime, key, value) end)
      restart_tracker()
    end)

    :ok
  end

  test "accepted and rejected tracks preserve per-user, per-IP and global limits" do
    assert Connections.total_local() == 0
    assert :ok = Connections.track("a", ip: "ip1")
    assert :ok = Connections.track("a", ip: "ip1")
    assert {:error, :limit_exceeded} = Connections.track("a", ip: "ip2")
    assert :ok = Connections.track("b", ip: "ip1")
    assert {:error, :ip_limit_exceeded} = Connections.track("c", ip: "ip1")
    assert :ok = Connections.track("c")
    assert {:error, :global_limit_exceeded} = Connections.track("d")
    assert Connections.count("a") == 2
    assert Connections.count_ip("ip1") == 3
    assert_total(4)

    Connections.untrack("a", ip: "ip1")
    assert_total(3)
    assert :ok = Connections.track("d", ip: "ip1")
    assert_total(4)
  end

  test "untracking an absent user and repeated cleanup never decrement the total twice" do
    assert :ok = Connections.track("a")
    Connections.untrack("absent")
    assert_total(1)
    Connections.untrack("a")
    Connections.untrack("a")
    assert_total(0)
  end

  test "process DOWN and sweep cleanup agree with the remaining user counts" do
    pid =
      spawn(fn ->
        receive do
          :stop -> :ok
        end
      end)

    assert :ok = Connections.track("a", pid: pid, ip: "ip1")
    assert :ok = Connections.track("b")
    assert_total(2)
    ref = Process.monitor(pid)
    send(pid, :stop)
    assert_receive {:DOWN, ^ref, :process, ^pid, _}
    send(Connections, :sweep)
    assert_total(1)
    assert Connections.count("a") == 0
    assert Connections.count_ip("ip1") == 0

    Connections.untrack("b")
    assert_total(0)
  end

  test "explicit cleanup followed by owner exit does not underflow" do
    pid =
      spawn(fn ->
        receive do
          :stop -> :ok
        end
      end)

    assert :ok = Connections.track("a", pid: pid)
    Connections.untrack("a")
    assert_total(0)
    ref = Process.monitor(pid)
    send(pid, :stop)
    assert_receive {:DOWN, ^ref, :process, ^pid, _}
    send(Connections, :sweep)
    assert_total(0)
  end

  test "concurrent owners reserve and release the exact number of slots" do
    Application.put_env(:realtime, :max_connections_global, 100)
    parent = self()

    pids =
      for id <- 1..40 do
        spawn(fn ->
          result = Connections.track("user-#{id}")
          send(parent, {:tracked, self(), result})

          receive do
            :stop -> :ok
          end
        end)
      end

    for pid <- pids, do: assert_receive({:tracked, ^pid, :ok})
    assert_total(40)

    for pid <- pids do
      ref = Process.monitor(pid)
      send(pid, :stop)
      assert_receive {:DOWN, ^ref, :process, ^pid, _}
    end

    send(Connections, :sweep)
    assert_total(0)
  end

  defp assert_total(expected) do
    :sys.get_state(Connections)
    assert Connections.total_local() == expected
    assert :ets.foldl(fn {_, count}, acc -> acc + count end, 0, :realtime_connections) == expected
  end

  defp restart_tracker do
    :ok = Supervisor.terminate_child(Realtime.Supervisor, Connections)
    {:ok, _} = Supervisor.restart_child(Realtime.Supervisor, Connections)
  end
end
