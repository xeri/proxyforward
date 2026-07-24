import {useEffect, useMemo, useState} from 'react'
import {DeleteGroup, ListGroups, SaveGroup, SetTunnelGroup} from '../../wailsjs/go/app/App'
import {gateway} from '../../wailsjs/go/models'
import {usePolled} from '../hooks'
import {UIStatus} from '../state'
import {
  Badge, Button, Card, EmptyState, ErrorBanner, Field, IconButton, Modal, MonoChip, Overline,
  SegmentedControl, Select, Skeleton, StatusDot, TextInput,
} from './ui'
import {IconBroadcast, IconEdit, IconPlus, IconServer, IconTrash} from './icons'

type GroupStatus = gateway.GroupStatus
type GroupSpec = gateway.GroupSpec
type AgentView = gateway.AgentView

// A group card reads as quiet trench glass, never the bloom frame: a group is a set
// of equal member cards, and N equal surfaces can't each answer the pointer (DESIGN
// rule 1/2). This whole surface belongs to the Agents (Fleet) family.

const POLICY_LABEL: Record<string, string> = {spread: 'spread', standby: 'standby'}
const POLICY_HINT: Record<string, string> = {
  spread: 'new players go to the member with the fewest connections',
  standby: 'all players go to one primary; the next takes over if it dies',
}

const groupsCache = new Map<string, GroupStatus[]>()

// nameOf resolves a member's display name from the roster: nickname, else hostname,
// else the raw id — never blank (mirrors the roster's displayName).
function nameOf(roster: AgentView[], agentId: string): string {
  const v = roster.find(r => r.agentId === agentId)
  return v ? v.nickname || v.hostname || agentId : agentId
}

/** GroupsPanel: the gateway's tunnel-group surface — a section within the Agents
 * screen. Lists each group with its live members (eligibility, standby primary,
 * live-conn load), and drives create / edit / delete and member tagging. All four
 * data states: skeleton, cards, a written empty state, and — off the gateway role —
 * nothing at all (the Agents screen already shows its own unavailable state). */
export function GroupsPanel({status, roster, onChanged}: {
  status: UIStatus; roster: AgentView[]; onChanged: () => void
}) {
  const [nonce, setNonce] = useState(0)
  const [editing, setEditing] = useState<GroupSpec | 'new' | null>(null)
  const isGateway = status.role === 'gateway'
  const groups = usePolled(groupsCache, isGateway ? `groups:${nonce}` : null, () => ListGroups(), 4000)

  // A mutation re-polls at once without flashing the skeleton: carry the current
  // groups into the next key, bump the local nonce, and bubble up so the roster and
  // its tunnel counts refresh too (tagging moves a tunnel off its solo listener).
  const refresh = () => {
    if (groups) groupsCache.set(`groups:${nonce + 1}`, groups)
    setNonce(n => n + 1)
    onChanged()
  }

  if (!isGateway) return null

  return (
    <section className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <Overline>load balancing</Overline>
          <p className="mt-0.5 text-xs italic text-[var(--text-3)]">
            Several agents behind one public port, for redundancy or spreading players.
          </p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => setEditing('new')}>
          <IconPlus size={14} /> New group
        </Button>
      </div>

      {groups === null ? (
        <div className="grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2">
          <Skeleton className="h-40 rounded-[var(--r-card)]" />
          <Skeleton className="h-40 rounded-[var(--r-card)]" />
        </div>
      ) : groups.length === 0 ? (
        <Card>
          <EmptyState
            icon={<IconBroadcast size={26} />}
            title="No groups yet"
            hint="A group puts several agents behind one public port — players spread across healthy members, or fail over to a standby. Members must share world state (a proxy or mirrored lobbies)."
            action={<Button size="sm" onClick={() => setEditing('new')}>New group</Button>}
          />
        </Card>
      ) : (
        <div className="pf-stagger grid grid-cols-1 gap-[var(--grid-gap)] @min-[52rem]:grid-cols-2 @min-[92rem]:grid-cols-3">
          {groups.map(g => (
            <GroupCard key={g.spec.id} group={g} roster={roster} status={status} onEdit={() => setEditing(g.spec)} onChanged={refresh} />
          ))}
        </div>
      )}

      {editing && (
        <GroupEditModal
          spec={editing === 'new' ? null : editing}
          status={status} roster={roster}
          onClose={() => setEditing(null)} onChanged={refresh}
        />
      )}
    </section>
  )
}

/** GroupCard: one group as quiet trench glass. Header names the group, its policy,
 * and its shared port; the body is a hairline list of members with per-member
 * eligibility, the standby primary marker, and live-connection load; the footer tags
 * another tunnel in. When no member is eligible the card says so — the port still
 * answers (offline responder), it just has nobody to route to. */
function GroupCard({group, roster, status, onEdit, onChanged}: {
  group: GroupStatus; roster: AgentView[]; status: UIStatus; onEdit: () => void; onChanged: () => void
}) {
  const {spec, members} = group
  const [adding, setAdding] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)
  const [err, setErr] = useState('')
  const eligible = members.filter(m => m.eligible).length

  const untag = async (agentId: string, tunnelId: string) => {
    setErr('')
    try { await SetTunnelGroup(agentId, tunnelId, ''); onChanged() }
    catch (e) { setErr(String(e)) }
  }

  return (
    <Card>
      <div className="space-y-3">
        {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}

        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="truncate font-display text-title [font-weight:var(--w-display-quiet)] text-[var(--text)]">
              {spec.name || 'Untitled group'}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              <Badge tone="accent">{POLICY_LABEL[spec.policy] ?? spec.policy}</Badge>
              <MonoChip>:{spec.publicPort}</MonoChip>
              <span className="text-caption text-[var(--text-3)]">
                {eligible} of {members.length} live
              </span>
            </div>
          </div>
          <div className="flex shrink-0 items-center">
            <IconButton title="Edit group" onClick={onEdit}><IconEdit size={15} /></IconButton>
            <IconButton title="Delete group" variant="danger" onClick={() => setConfirmDel(true)}><IconTrash size={15} /></IconButton>
          </div>
        </div>

        {members.length === 0 ? (
          <p className="py-2 text-xs italic text-[var(--text-3)]">
            No members yet. Tag a tunnel into this group to start serving it on port {spec.publicPort}.
          </p>
        ) : (
          <ul className="-mx-1">
            {members.map(m => (
              <li key={`${m.agentId}:${m.tunnelId}`} className="flex items-center gap-2 border-t border-[var(--hairline)] px-1 py-2 first:border-t-0">
                <StatusDot state={m.eligible ? 'good' : 'unknown'} label="" pulse={m.eligible} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate text-body text-[var(--text)]">{nameOf(roster, m.agentId)}</span>
                    {m.isPrimary && <Badge tone="good">primary</Badge>}
                  </div>
                  <div className="truncate text-caption text-[var(--text-3)]">
                    {m.eligible ? `${m.liveConns} ${m.liveConns === 1 ? 'connection' : 'connections'}` : 'not eligible — cold, down, or paused'}
                  </div>
                </div>
                <IconButton title="Remove from group" onClick={() => untag(m.agentId, m.tunnelId)}><IconTrash size={14} /></IconButton>
              </li>
            ))}
          </ul>
        )}

        {members.length > 0 && eligible === 0 && (
          <p className="text-xs italic text-[var(--warn)]">
            No eligible member — the port answers the offline responder until one comes up.
          </p>
        )}

        <button
          onClick={() => setAdding(true)}
          className="pf-press inline-flex items-center gap-1.5 text-sm text-[var(--text-3)] transition-colors hover:text-[var(--text)]"
        >
          <IconPlus size={14} /> Add member
        </button>
      </div>

      {adding && (
        <AddMemberModal
          group={spec} status={status} roster={roster}
          onClose={() => setAdding(false)} onChanged={onChanged}
        />
      )}
      {confirmDel && (
        <Modal
          title="Delete group?"
          onClose={() => setConfirmDel(false)}
          footer={<>
            <Button variant="ghost" size="sm" onClick={() => setConfirmDel(false)}>Cancel</Button>
            <Button variant="danger" size="sm" onClick={async () => {
              setErr('')
              try { await DeleteGroup(spec.id); setConfirmDel(false); onChanged() }
              catch (e) { setErr(String(e)); setConfirmDel(false) }
            }}>Delete</Button>
          </>}
        >
          <p className="text-body text-[var(--text-2)]">
            The shared port {spec.publicPort} stops accepting players and in-flight players are dropped.
            The {members.length} member{members.length === 1 ? '' : 's'} revert to their own ports.
          </p>
        </Modal>
      )}
    </Card>
  )
}

/** GroupEditModal: create (spec=null) or edit a group's config — name, shared public
 * port, policy, and the offline message shown when no member is eligible. Grouping is
 * arbitration, so a save applies immediately; a validation or port-conflict error
 * surfaces verbatim. */
function GroupEditModal({spec, status, roster, onClose, onChanged}: {
  spec: GroupSpec | null; status: UIStatus; roster: AgentView[]; onClose: () => void; onChanged: () => void
}) {
  const [name, setName] = useState(spec?.name ?? '')
  const [port, setPort] = useState(spec ? String(spec.publicPort) : '')
  const [policy, setPolicy] = useState<'spread' | 'standby'>((spec?.policy as 'spread' | 'standby') || 'spread')
  const [motd, setMotd] = useState(spec?.offlineMotd ?? '')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const portNum = parseInt(port.trim(), 10)
  const valid = name.trim() !== '' && portNum > 0 && portNum < 65536

  const save = async () => {
    if (!valid) return
    setSaving(true); setErr('')
    try { await SaveGroup(spec?.id ?? '', name.trim(), portNum, policy, motd.trim()); onChanged(); onClose() }
    catch (e) { setErr(String(e)); setSaving(false) }
  }

  return (
    <Modal
      title={spec ? 'Edit group' : 'New group'}
      onClose={onClose}
      footer={<>
        <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        <Button size="sm" onClick={save} disabled={!valid || saving}>{saving ? 'Saving…' : spec ? 'Save' : 'Create'}</Button>
      </>}
    >
      <div className="space-y-4">
        {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}
        <Field label="Name" hint="Shown on the group card. Players never see it.">
          <TextInput value={name} onChange={setName} placeholder="Survival cluster" autoFocus />
        </Field>
        <Field label="Public port" hint="The one port players connect to. Members serve behind it instead of their own ports.">
          <TextInput value={port} onChange={setPort} placeholder="25565" type="text" mono />
        </Field>
        <Field label="Policy" hint={POLICY_HINT[policy]}>
          <SegmentedControl<'spread' | 'standby'>
            value={policy} onChange={setPolicy}
            options={[{value: 'spread', label: 'Spread'}, {value: 'standby', label: 'Standby'}]}
          />
        </Field>
        <Field label="Offline message" hint="Shown to players in the server list when no member is available (optional).">
          <TextInput value={motd} onChange={setMotd} placeholder="Back soon" />
        </Field>
        {!spec && (
          <p className="text-xs italic text-[var(--text-3)]">
            After creating the group, add member tunnels from its card. Members must share world state — a
            Velocity/Bungee proxy behind each, or mirrored lobbies.
          </p>
        )}
      </div>
    </Modal>
  )
}

/** AddMemberModal: tag one of the fleet's solo tunnels into the group. Only tunnels
 * not already in a group are candidates (a grouped tunnel has no solo listener, so it
 * isn't in the live tunnel table). */
function AddMemberModal({group, status, roster, onClose, onChanged}: {
  group: GroupSpec; status: UIStatus; roster: AgentView[]; onClose: () => void; onChanged: () => void
}) {
  const [pick, setPick] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  // Candidates: every currently-bound solo tunnel, labeled by its agent. Keyed
  // agentId:tunnelId so two agents' same tunnelId never collide.
  const candidates = useMemo(() => (status.tunnels ?? [])
    .filter(t => t.agentId)
    .map(t => ({key: `${t.agentId}:${t.id}`, agentId: t.agentId!, tunnelId: t.id,
      label: `${nameOf(roster, t.agentId!)} · ${t.name} (:${t.publicPort})`})),
    [status.tunnels, roster])

  useEffect(() => { if (!pick && candidates.length) setPick(candidates[0].key) }, [candidates, pick])

  const add = async () => {
    const c = candidates.find(x => x.key === pick)
    if (!c) return
    setSaving(true); setErr('')
    try { await SetTunnelGroup(c.agentId, c.tunnelId, group.id); onChanged(); onClose() }
    catch (e) { setErr(String(e)); setSaving(false) }
  }

  return (
    <Modal
      title="Add member"
      onClose={onClose}
      footer={<>
        <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        <Button size="sm" onClick={add} disabled={!pick || saving}>{saving ? 'Adding…' : 'Add'}</Button>
      </>}
    >
      <div className="space-y-4">
        {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}
        {candidates.length === 0 ? (
          <EmptyState
            icon={<IconServer size={24} />}
            title="No tunnels to add"
            hint="Every live tunnel is already grouped, or no agent is serving one. A member's tunnel must be enabled and its scope must allow the group's port."
          />
        ) : (
          <Field label="Tunnel" hint={`Tagging a tunnel binds it behind ${group.name || 'this group'} on port ${group.publicPort}; its own port is released.`}>
            <Select value={pick} onChange={setPick} options={candidates.map(c => ({value: c.key, label: c.label}))} />
          </Field>
        )}
      </div>
    </Modal>
  )
}
