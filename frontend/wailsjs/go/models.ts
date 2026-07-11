export namespace app {
	
	export class ConnUI {
	    id: number;
	    tunnelName: string;
	    clientAddr: string;
	    startedAt: number;
	    bytesIn: number;
	    bytesOut: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnUI(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tunnelName = source["tunnelName"];
	        this.clientAddr = source["clientAddr"];
	        this.startedAt = source["startedAt"];
	        this.bytesIn = source["bytesIn"];
	        this.bytesOut = source["bytesOut"];
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

