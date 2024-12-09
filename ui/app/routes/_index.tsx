import { useLoaderData, useActionData, Form, useSearchParams } from "@remix-run/react";
import type { LoaderFunctionArgs, ActionFunctionArgs } from "@remix-run/node";
import { json, redirect } from "@remix-run/node";

interface Peer {
  id: string;
  address: string;
  voter: string;
}

type ActionData = {
  keyValue?: string;
  error?: string;
}

export async function loader({ request }: LoaderFunctionArgs) {
  const url = new URL(request.url);
  const leaderAddr = url.searchParams.get("leaderAddr") || "http://localhost:8081";

  // リーダーノードがダウンしている場合を想定して、try-catchでエラーを拾います。
  let leader = "";
  let peers: Peer[] = [];
  let stats: Record<string,string> = {};
  let fooValue = "";
  let leaderError = "";
  
  try {
    const [leaderRes, peersRes, statsRes, fooRes] = await Promise.all([
      fetch(`${leaderAddr}/leader`),
      fetch(`${leaderAddr}/peers`),
      fetch(`${leaderAddr}/stats`),
      fetch(`${leaderAddr}/get?key=foo`)
    ]);

    if (!leaderRes.ok || !peersRes.ok || !statsRes.ok || !fooRes.ok) {
      throw new Error("Failed to fetch cluster info.");
    }

    leader = await leaderRes.text();
    peers = await peersRes.json();
    stats = await statsRes.json();
    fooValue = await fooRes.text();
  } catch (e) {
    leaderError = `Could not fetch from leaderAddr: ${leaderAddr}. Check if the node is up and reachable.`;
  }

  return json({ leaderAddr, leader, peers, stats, fooValue, leaderError });
}

export async function action({ request }: ActionFunctionArgs) {
  const formData = await request.formData();
  const leaderAddr = formData.get("leaderAddr");
  if (typeof leaderAddr !== "string" || !leaderAddr.startsWith("http")) {
    return json<ActionData>({error: "Invalid leader address"});
  }

  const intent = formData.get("intent");
  if (intent === "changeLeader") {
    // leaderAddr変更時はそのままリダイレクト
    return redirect(`/?leaderAddr=${encodeURIComponent(leaderAddr)}`);
  }

  if (intent === "set") {
    const key = formData.get("key");
    const value = formData.get("value");
    if (typeof key !== "string" || !key || typeof value !== "string" || !value) {
      return json<ActionData>({error: "Key and value are required."});
    }
    const res = await fetch(`${leaderAddr}/set?key=${encodeURIComponent(key)}&value=${encodeURIComponent(value)}`);
    if (!res.ok) {
      return json<ActionData>({error: "Failed to set key-value."});
    }
    return json<ActionData>({});
  } else if (intent === "get") {
    const key = formData.get("key");
    if (typeof key !== "string" || !key) {
      return json<ActionData>({error: "Key is required."});
    }
    const res = await fetch(`${leaderAddr}/get?key=${encodeURIComponent(key)}`);
    if (!res.ok) {
      return json<ActionData>({error: "Failed to get key-value."});
    }
    const val = await res.text();
    return json<ActionData>({keyValue: val});
  } else if (intent === "remove") {
    const id = formData.get("id");
    if (typeof id !== "string" || !id) {
      return json<ActionData>({error: "Node ID is required to remove."});
    }
    const res = await fetch(`${leaderAddr}/remove?id=${encodeURIComponent(id)}`);
    if (!res.ok) {
      return json<ActionData>({error: "Failed to remove peer."});
    }
    return json<ActionData>({});
  }

  return null;
}

export default function Index() {
  const { leaderAddr, leader, peers, stats, fooValue, leaderError } = useLoaderData<typeof loader>();
  const actionData = useActionData<ActionData>();
  const [searchParams] = useSearchParams();

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <h1 className="text-2xl font-bold mb-4">Raft Cluster Dashboard</h1>

      {/* Leaderアドレス変更フォーム */}
      <Form method="post" className="mb-4 flex space-x-2 items-center">
        <input
          type="text"
          name="leaderAddr"
          defaultValue={leaderAddr}
          className="border p-2 rounded w-80"
          placeholder="http://raft1:8080"
        />
        <button
          type="submit"
          name="intent"
          value="changeLeader"
          className="px-4 py-2 bg-yellow-600 text-white rounded hover:bg-yellow-700"
        >
          Change Leader Node
        </button>
      </Form>

      {leaderError && (
        <p className="text-red-700 mb-4">{leaderError}</p>
      )}

      <div className="flex space-x-2 mb-4">
        <Form method="get">
          <input type="hidden" name="leaderAddr" value={leaderAddr}/>
          <button 
            type="submit"
            className="px-4 py-2 bg-gray-300 rounded hover:bg-gray-400"
          >
            Refresh
          </button>
        </Form>
      </div>

      <h2 className="text-xl font-semibold mt-6 mb-2">Set arbitrary key-value</h2>
      <Form method="post" className="flex space-x-2 mb-4">
        <input type="hidden" name="leaderAddr" value={leaderAddr}/>
        <input
          type="text"
          name="key"
          className="border p-2 rounded"
          placeholder="key"
        />
        <input
          type="text"
          name="value"
          className="border p-2 rounded"
          placeholder="value"
        />
        <button
          type="submit"
          name="intent"
          value="set"
          className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
        >
          Set
        </button>
      </Form>

      <h2 className="text-xl font-semibold mt-6 mb-2">Get arbitrary key</h2>
      <Form method="post" className="flex space-x-2 mb-4">
        <input type="hidden" name="leaderAddr" value={leaderAddr}/>
        <input
          type="text"
          name="key"
          className="border p-2 rounded"
          placeholder="key"
        />
        <button
          type="submit"
          name="intent"
          value="get"
          className="px-4 py-2 bg-green-600 text-white rounded hover:bg-green-700"
        >
          Get
        </button>
      </Form>

      {actionData?.keyValue !== undefined && (
        <p className="mb-4">Key Value: <span className="font-mono">{actionData.keyValue}</span></p>
      )}
      {actionData?.error && (
        <p className="text-red-700 mb-4">{actionData.error}</p>
      )}

      <h2 className="text-xl font-semibold mt-6 mb-2">Leader</h2>
      <p className="mb-4">{leader}</p>

      <h2 className="text-xl font-semibold mt-6 mb-2">Stats</h2>
      <table className="border-collapse border border-gray-300 w-full text-sm mb-4">
        <tbody>
          {Object.entries(stats).map(([k,v]) => (
            <tr key={k} className="border-b border-gray-200">
              <th className="text-left p-2 font-medium bg-gray-100">{k}</th>
              <td className="p-2">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2 className="text-xl font-semibold mt-6 mb-2">Peers</h2>
      <table className="border-collapse border border-gray-300 w-full text-sm mb-4">
        <thead>
          <tr className="border-b border-gray-200 bg-gray-100">
            <th className="p-2 text-left">ID</th>
            <th className="p-2 text-left">Address</th>
            <th className="p-2 text-left">Voter</th>
            <th className="p-2">Actions</th>
          </tr>
        </thead>
        <tbody>
          {peers.map((p) => (
            <tr key={p.id} className="border-b border-gray-200">
              <td className="p-2">{p.id}</td>
              <td className="p-2">{p.address}</td>
              <td className="p-2">{p.voter}</td>
              <td className="p-2 text-center">
                <Form method="post">
                  <input type="hidden" name="leaderAddr" value={leaderAddr}/>
                  <input type="hidden" name="id" value={p.id}/>
                  <button
                    type="submit"
                    name="intent"
                    value="remove"
                    className="px-3 py-1 bg-red-600 text-white rounded hover:bg-red-700"
                  >
                    Remove
                  </button>
                </Form>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2 className="text-xl font-semibold mt-6 mb-2">Get foo (example)</h2>
      <p className="mb-4">{fooValue}</p>
    </div>
  );
}
