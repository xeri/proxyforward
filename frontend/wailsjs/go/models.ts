export namespace analytics {
	
	export class CountryAgg {
	    cc: string;
	    country: string;
	    players: number;
	    sessions: number;
	    bytesIn: number;
	    bytesOut: number;
	    rttAvg: number;
	
	    static createFrom(source: any = {}) {
	        return new CountryAgg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cc = source["cc"];
	        this.country = source["country"];
	        this.players = source["players"];
	        this.sessions = source["sessions"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.rttAvg = source["rttAvg"];
	    }
	}
	export class IPSpan {
	    ip: string;
	    firstSeen: number;
	    lastSeen: number;
	    sessions: number;
	    cc: string;
	
	    static createFrom(source: any = {}) {
	        return new IPSpan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	        this.sessions = source["sessions"];
	        this.cc = source["cc"];
	    }
	}
	export class LatencyPoint {
	    t: number;
	    avg: number;
	    min: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new LatencyPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t = source["t"];
	        this.avg = source["avg"];
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class NameSpan {
	    name: string;
	    firstSeen: number;
	    lastSeen: number;
	
	    static createFrom(source: any = {}) {
	        return new NameSpan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	    }
	}
	export class PeakCell {
	    avg: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new PeakCell(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.avg = source["avg"];
	        this.max = source["max"];
	    }
	}
	export class PeakMatrix {
	    cells: PeakCell[][];
	
	    static createFrom(source: any = {}) {
	        return new PeakMatrix(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cells = this.convertValues(source["cells"], PeakCell);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlayerCard {
	    uuid: string;
	    name: string;
	    offline: boolean;
	    online: boolean;
	    firstSeen: number;
	    lastSeen: number;
	    sessions: number;
	    playMs: number;
	    bytesIn: number;
	    bytesOut: number;
	    lastCc: string;
	    rttMs: number;
	
	    static createFrom(source: any = {}) {
	        return new PlayerCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.name = source["name"];
	        this.offline = source["offline"];
	        this.online = source["online"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	        this.sessions = source["sessions"];
	        this.playMs = source["playMs"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.lastCc = source["lastCc"];
	        this.rttMs = source["rttMs"];
	    }
	}
	export class SessionMeta {
	    id: number;
	    tunnelName: string;
	    clientIp: string;
	    playerName: string;
	    playerUuid: string;
	    startedMs: number;
	    endedMs: number;
	    bytesIn: number;
	    bytesOut: number;
	    cc: string;
	    rttAvg: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tunnelName = source["tunnelName"];
	        this.clientIp = source["clientIp"];
	        this.playerName = source["playerName"];
	        this.playerUuid = source["playerUuid"];
	        this.startedMs = source["startedMs"];
	        this.endedMs = source["endedMs"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.cc = source["cc"];
	        this.rttAvg = source["rttAvg"];
	    }
	}
	export class PlayerDetail {
	    card: PlayerCard;
	    names: NameSpan[];
	    ips: IPSpan[];
	    recent: SessionMeta[];
	
	    static createFrom(source: any = {}) {
	        return new PlayerDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.card = this.convertValues(source["card"], PlayerCard);
	        this.names = this.convertValues(source["names"], NameSpan);
	        this.ips = this.convertValues(source["ips"], IPSpan);
	        this.recent = this.convertValues(source["recent"], SessionMeta);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlayersPage {
	    total: number;
	    players: PlayerCard[];
	
	    static createFrom(source: any = {}) {
	        return new PlayersPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.players = this.convertValues(source["players"], PlayerCard);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlayersQuery {
	    search: string;
	    sort: string;
	    agentId: string;
	    tunnelId: string;
	    cc: string;
	    offset: number;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new PlayersQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.search = source["search"];
	        this.sort = source["sort"];
	        this.agentId = source["agentId"];
	        this.tunnelId = source["tunnelId"];
	        this.cc = source["cc"];
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	    }
	}
	
	export class TrafficPoint {
	    t: number;
	    in: number;
	    out: number;
	
	    static createFrom(source: any = {}) {
	        return new TrafficPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t = source["t"];
	        this.in = source["in"];
	        this.out = source["out"];
	    }
	}
	export class SessionTimeline {
	    traffic: TrafficPoint[];
	    rtt: LatencyPoint[];
	
	    static createFrom(source: any = {}) {
	        return new SessionTimeline(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.traffic = this.convertValues(source["traffic"], TrafficPoint);
	        this.rtt = this.convertValues(source["rtt"], LatencyPoint);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionsPage {
	    total: number;
	    sessions: SessionMeta[];
	
	    static createFrom(source: any = {}) {
	        return new SessionsPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.sessions = this.convertValues(source["sessions"], SessionMeta);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionsQuery {
	    playerUuid: string;
	    agentId: string;
	    tunnelId: string;
	    cc: string;
	    sinceMs: number;
	    offset: number;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionsQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playerUuid = source["playerUuid"];
	        this.agentId = source["agentId"];
	        this.tunnelId = source["tunnelId"];
	        this.cc = source["cc"];
	        this.sinceMs = source["sinceMs"];
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	    }
	}
	export class Summary {
	    rangeMs: number;
	    bytesIn: number;
	    bytesOut: number;
	    sessions: number;
	    uniquePlayers: number;
	    peakPlayers: number;
	    peakPlayersAt: number;
	    peakInBps: number;
	    peakInAt: number;
	    peakOutBps: number;
	    peakOutAt: number;
	    avgRttMs: number;
	    avgLossPct: number;
	    linkUptimePct: number;
	    recInBps: number;
	    recInAt: number;
	    recOutBps: number;
	    recOutAt: number;
	    recPlayers: number;
	    recPlayersAt: number;
	    recConns: number;
	    recConnsAt: number;
	    lifetimeBytesIn: number;
	    lifetimeBytesOut: number;
	    lifetimeUptimeMs: number;
	    linkSessions: number;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rangeMs = source["rangeMs"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.sessions = source["sessions"];
	        this.uniquePlayers = source["uniquePlayers"];
	        this.peakPlayers = source["peakPlayers"];
	        this.peakPlayersAt = source["peakPlayersAt"];
	        this.peakInBps = source["peakInBps"];
	        this.peakInAt = source["peakInAt"];
	        this.peakOutBps = source["peakOutBps"];
	        this.peakOutAt = source["peakOutAt"];
	        this.avgRttMs = source["avgRttMs"];
	        this.avgLossPct = source["avgLossPct"];
	        this.linkUptimePct = source["linkUptimePct"];
	        this.recInBps = source["recInBps"];
	        this.recInAt = source["recInAt"];
	        this.recOutBps = source["recOutBps"];
	        this.recOutAt = source["recOutAt"];
	        this.recPlayers = source["recPlayers"];
	        this.recPlayersAt = source["recPlayersAt"];
	        this.recConns = source["recConns"];
	        this.recConnsAt = source["recConnsAt"];
	        this.lifetimeBytesIn = source["lifetimeBytesIn"];
	        this.lifetimeBytesOut = source["lifetimeBytesOut"];
	        this.lifetimeUptimeMs = source["lifetimeUptimeMs"];
	        this.linkSessions = source["linkSessions"];
	    }
	}
	
	export class UptimeSpan {
	    t: number;
	    up: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UptimeSpan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t = source["t"];
	        this.up = source["up"];
	    }
	}
	export class TunnelUptime {
	    tunnelId: string;
	    name: string;
	    uptimePct: number;
	    events: UptimeSpan[];
	
	    static createFrom(source: any = {}) {
	        return new TunnelUptime(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tunnelId = source["tunnelId"];
	        this.name = source["name"];
	        this.uptimePct = source["uptimePct"];
	        this.events = this.convertValues(source["events"], UptimeSpan);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UptimeReport {
	    link: TunnelUptime;
	    tunnels: TunnelUptime[];
	
	    static createFrom(source: any = {}) {
	        return new UptimeReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.link = this.convertValues(source["link"], TunnelUptime);
	        this.tunnels = this.convertValues(source["tunnels"], TunnelUptime);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace app {
	
	export class AgentUI {
	    agentId: string;
	    hostname: string;
	    lanIps: string[];
	    remoteIp: string;
	    linkUpSinceMs: number;
	    rttMillis: number;
	    jitterMillis: number;
	    packetLossPct: number;
	    healthScore: string;
	    linkBytesIn: number;
	    linkBytesOut: number;
	    tunnels: number;
	    players: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentUI(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agentId = source["agentId"];
	        this.hostname = source["hostname"];
	        this.lanIps = source["lanIps"];
	        this.remoteIp = source["remoteIp"];
	        this.linkUpSinceMs = source["linkUpSinceMs"];
	        this.rttMillis = source["rttMillis"];
	        this.jitterMillis = source["jitterMillis"];
	        this.packetLossPct = source["packetLossPct"];
	        this.healthScore = source["healthScore"];
	        this.linkBytesIn = source["linkBytesIn"];
	        this.linkBytesOut = source["linkBytesOut"];
	        this.tunnels = source["tunnels"];
	        this.players = source["players"];
	    }
	}
	export class ConnUI {
	    id: number;
	    agentId?: string;
	    tunnelName: string;
	    clientAddr: string;
	    startedAt: number;
	    bytesIn: number;
	    bytesOut: number;
	    playerName?: string;
	    playerUuid?: string;
	    rttMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnUI(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.agentId = source["agentId"];
	        this.tunnelName = source["tunnelName"];
	        this.clientAddr = source["clientAddr"];
	        this.startedAt = source["startedAt"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
	        this.playerName = source["playerName"];
	        this.playerUuid = source["playerUuid"];
	        this.rttMs = source["rttMs"];
	    }
	}
	export class LatencyResult {
	    samples: number;
	    rttAvgMs: number;
	    rttMinMs: number;
	    rttMaxMs: number;
	    jitterMs: number;
	    oneWayEstimateMs: number;
	    oneWayUpMs: number;
	    oneWayDownMs: number;
	    haveOneWay: boolean;
	    clockSyncCaveat: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LatencyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.samples = source["samples"];
	        this.rttAvgMs = source["rttAvgMs"];
	        this.rttMinMs = source["rttMinMs"];
	        this.rttMaxMs = source["rttMaxMs"];
	        this.jitterMs = source["jitterMs"];
	        this.oneWayEstimateMs = source["oneWayEstimateMs"];
	        this.oneWayUpMs = source["oneWayUpMs"];
	        this.oneWayDownMs = source["oneWayDownMs"];
	        this.haveOneWay = source["haveOneWay"];
	        this.clockSyncCaveat = source["clockSyncCaveat"];
	    }
	}
	export class SetupFileInfo {
	    path: string;
	    role: string;
	    appVersion: string;
	    exportedAtMs: number;
	    encrypted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SetupFileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.role = source["role"];
	        this.appVersion = source["appVersion"];
	        this.exportedAtMs = source["exportedAtMs"];
	        this.encrypted = source["encrypted"];
	    }
	}
	export class TunnelUI {
	    agentId?: string;
	    id: string;
	    name: string;
	    publicPort: number;
	    localUp: boolean;
	    localKnown: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TunnelUI(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agentId = source["agentId"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.publicPort = source["publicPort"];
	        this.localUp = source["localUp"];
	        this.localKnown = source["localKnown"];
	    }
	}
	export class UIStatus {
	    mode: string;
	    role: string;
	    version: string;
	    hostname: string;
	    pid: number;
	    configPath: string;
	    linkUp: boolean;
	    rttMillis: number;
	    agentConnected: boolean;
	    jitterMillis: number;
	    packetLossPct: number;
	    healthScore: string;
	    peerHostname: string;
	    publicIp: string;
	    peerPublicIp: string;
	    localLanIps: string[];
	    peerLanIps: string[];
	    agents: AgentUI[];
	    tunnels: TunnelUI[];
	    connections: ConnUI[];
	    totalBytesIn: number;
	    totalBytesOut: number;
	    linkUpSinceMs: number;
	    processStartMs: number;
	    peerAddr: string;
	    linkBytesIn: number;
	    linkBytesOut: number;
	    allTimeBytesIn: number;
	    allTimeBytesOut: number;
	    cumulativeUptimeMs: number;
	    linkSessions: number;
	    historyUnsupported?: boolean;
	    analyticsUnsupported?: boolean;
	    connectionsTruncated?: boolean;
	    connectionsTotal?: number;
	    engineFatal?: string;
	
	    static createFrom(source: any = {}) {
	        return new UIStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.role = source["role"];
	        this.version = source["version"];
	        this.hostname = source["hostname"];
	        this.pid = source["pid"];
	        this.configPath = source["configPath"];
	        this.linkUp = source["linkUp"];
	        this.rttMillis = source["rttMillis"];
	        this.agentConnected = source["agentConnected"];
	        this.jitterMillis = source["jitterMillis"];
	        this.packetLossPct = source["packetLossPct"];
	        this.healthScore = source["healthScore"];
	        this.peerHostname = source["peerHostname"];
	        this.publicIp = source["publicIp"];
	        this.peerPublicIp = source["peerPublicIp"];
	        this.localLanIps = source["localLanIps"];
	        this.peerLanIps = source["peerLanIps"];
	        this.agents = this.convertValues(source["agents"], AgentUI);
	        this.tunnels = this.convertValues(source["tunnels"], TunnelUI);
	        this.connections = this.convertValues(source["connections"], ConnUI);
	        this.totalBytesIn = source["totalBytesIn"];
	        this.totalBytesOut = source["totalBytesOut"];
	        this.linkUpSinceMs = source["linkUpSinceMs"];
	        this.processStartMs = source["processStartMs"];
	        this.peerAddr = source["peerAddr"];
	        this.linkBytesIn = source["linkBytesIn"];
	        this.linkBytesOut = source["linkBytesOut"];
	        this.allTimeBytesIn = source["allTimeBytesIn"];
	        this.allTimeBytesOut = source["allTimeBytesOut"];
	        this.cumulativeUptimeMs = source["cumulativeUptimeMs"];
	        this.linkSessions = source["linkSessions"];
	        this.historyUnsupported = source["historyUnsupported"];
	        this.analyticsUnsupported = source["analyticsUnsupported"];
	        this.connectionsTruncated = source["connectionsTruncated"];
	        this.connectionsTotal = source["connectionsTotal"];
	        this.engineFatal = source["engineFatal"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace config {
	
	export class TunnelOptions {
	    MinecraftAware: boolean;
	    ProxyProtocolV2: boolean;
	    OfflineMOTD: string;
	    BandwidthLimitMbps: number;
	
	    static createFrom(source: any = {}) {
	        return new TunnelOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.MinecraftAware = source["MinecraftAware"];
	        this.ProxyProtocolV2 = source["ProxyProtocolV2"];
	        this.OfflineMOTD = source["OfflineMOTD"];
	        this.BandwidthLimitMbps = source["BandwidthLimitMbps"];
	    }
	}
	export class Tunnel {
	    ID: string;
	    Name: string;
	    Type: string;
	    LocalAddr: string;
	    PublicPort: number;
	    Enabled: boolean;
	    Options: TunnelOptions;
	
	    static createFrom(source: any = {}) {
	        return new Tunnel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Type = source["Type"];
	        this.LocalAddr = source["LocalAddr"];
	        this.PublicPort = source["PublicPort"];
	        this.Enabled = source["Enabled"];
	        this.Options = this.convertValues(source["Options"], TunnelOptions);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentConfig {
	    AgentID: string;
	    GatewayHost: string;
	    GatewayPort: number;
	    Token: string;
	    CertFingerprint: string;
	    Transport: string;
	    Tunnels: Tunnel[];
	
	    static createFrom(source: any = {}) {
	        return new AgentConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.AgentID = source["AgentID"];
	        this.GatewayHost = source["GatewayHost"];
	        this.GatewayPort = source["GatewayPort"];
	        this.Token = source["Token"];
	        this.CertFingerprint = source["CertFingerprint"];
	        this.Transport = source["Transport"];
	        this.Tunnels = this.convertValues(source["Tunnels"], Tunnel);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalyticsConfig {
	    RetentionDays: number;
	    MojangLookups: boolean;
	    GeoIPCityPath: string;
	    GeoIPASNPath: string;
	
	    static createFrom(source: any = {}) {
	        return new AnalyticsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.RetentionDays = source["RetentionDays"];
	        this.MojangLookups = source["MojangLookups"];
	        this.GeoIPCityPath = source["GeoIPCityPath"];
	        this.GeoIPASNPath = source["GeoIPASNPath"];
	    }
	}
	export class UIConfig {
	    Theme: string;
	    MinimizeToTray: boolean;
	    Autostart: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Theme = source["Theme"];
	        this.MinimizeToTray = source["MinimizeToTray"];
	        this.Autostart = source["Autostart"];
	    }
	}
	export class LoggingConfig {
	    Level: string;
	    FileEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LoggingConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Level = source["Level"];
	        this.FileEnabled = source["FileEnabled"];
	    }
	}
	export class MetricsConfig {
	    PrometheusEnabled: boolean;
	    PrometheusAddr: string;
	
	    static createFrom(source: any = {}) {
	        return new MetricsConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.PrometheusEnabled = source["PrometheusEnabled"];
	        this.PrometheusAddr = source["PrometheusAddr"];
	    }
	}
	export class GatewayConfig {
	    BindAddr: string;
	    ControlPort: number;
	    Token: string;
	    PublicHost: string;
	    PortAllowlist: number[];
	    MaxConnsGlobal: number;
	    MaxConnsPerIP: number;
	    AuthAttemptsPerMin: number;
	
	    static createFrom(source: any = {}) {
	        return new GatewayConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.BindAddr = source["BindAddr"];
	        this.ControlPort = source["ControlPort"];
	        this.Token = source["Token"];
	        this.PublicHost = source["PublicHost"];
	        this.PortAllowlist = source["PortAllowlist"];
	        this.MaxConnsGlobal = source["MaxConnsGlobal"];
	        this.MaxConnsPerIP = source["MaxConnsPerIP"];
	        this.AuthAttemptsPerMin = source["AuthAttemptsPerMin"];
	    }
	}
	export class Config {
	    Role: string;
	    Agent: AgentConfig;
	    Gateway: GatewayConfig;
	    Metrics: MetricsConfig;
	    Logging: LoggingConfig;
	    UI: UIConfig;
	    Analytics: AnalyticsConfig;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Role = source["Role"];
	        this.Agent = this.convertValues(source["Agent"], AgentConfig);
	        this.Gateway = this.convertValues(source["Gateway"], GatewayConfig);
	        this.Metrics = this.convertValues(source["Metrics"], MetricsConfig);
	        this.Logging = this.convertValues(source["Logging"], LoggingConfig);
	        this.UI = this.convertValues(source["UI"], UIConfig);
	        this.Analytics = this.convertValues(source["Analytics"], AnalyticsConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	

}

export namespace geo {
	
	export class Status {
	    cityLoaded: boolean;
	    asnLoaded: boolean;
	    cityError?: string;
	    asnError?: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cityLoaded = source["cityLoaded"];
	        this.asnLoaded = source["asnLoaded"];
	        this.cityError = source["cityError"];
	        this.asnError = source["asnError"];
	    }
	}

}

export namespace logging {
	
	export class Entry {
	    seq: number;
	    timeMs: number;
	    level: string;
	    msg: string;
	    attrs: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.timeMs = source["timeMs"];
	        this.level = source["level"];
	        this.msg = source["msg"];
	        this.attrs = source["attrs"];
	    }
	}

}

export namespace stats {
	
	export class Bucket {
	    t: number;
	    in: number;
	    out: number;
	    io: number;
	    ih: number;
	    il: number;
	    ic: number;
	    oo: number;
	    oh: number;
	    ol: number;
	    oc: number;
	    co: number;
	    ch: number;
	    cl: number;
	    cc: number;
	    ro: number;
	    rh: number;
	    rl: number;
	    rc: number;
	    po: number;
	    ph: number;
	    pl: number;
	    pc: number;
	    lo: number;
	    lh: number;
	    ll: number;
	    lc: number;
	
	    static createFrom(source: any = {}) {
	        return new Bucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t = source["t"];
	        this.in = source["in"];
	        this.out = source["out"];
	        this.io = source["io"];
	        this.ih = source["ih"];
	        this.il = source["il"];
	        this.ic = source["ic"];
	        this.oo = source["oo"];
	        this.oh = source["oh"];
	        this.ol = source["ol"];
	        this.oc = source["oc"];
	        this.co = source["co"];
	        this.ch = source["ch"];
	        this.cl = source["cl"];
	        this.cc = source["cc"];
	        this.ro = source["ro"];
	        this.rh = source["rh"];
	        this.rl = source["rl"];
	        this.rc = source["rc"];
	        this.po = source["po"];
	        this.ph = source["ph"];
	        this.pl = source["pl"];
	        this.pc = source["pc"];
	        this.lo = source["lo"];
	        this.lh = source["lh"];
	        this.ll = source["ll"];
	        this.lc = source["lc"];
	    }
	}
	export class HistoryResult {
	    windowMs: number;
	    bucketMs: number;
	    buckets: Bucket[];
	
	    static createFrom(source: any = {}) {
	        return new HistoryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.windowMs = source["windowMs"];
	        this.bucketMs = source["bucketMs"];
	        this.buckets = this.convertValues(source["buckets"], Bucket);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PeerStat {
	    ip: string;
	    firstSeen: number;
	    lastSeen: number;
	    totalBytesIn: number;
	    totalBytesOut: number;
	    totalConns: number;
	
	    static createFrom(source: any = {}) {
	        return new PeerStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	        this.totalBytesIn = source["totalBytesIn"];
	        this.totalBytesOut = source["totalBytesOut"];
	        this.totalConns = source["totalConns"];
	    }
	}

}

