export namespace main {

	export class CleanupRecord {
	    id: string;
	    pid: number;
	    threads: number;
	    memoryMB: number;
	    cleanedAt: string;

	    static createFrom(source: any = {}) {
	        return new CleanupRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.pid = source["pid"];
	        this.threads = source["threads"];
	        this.memoryMB = source["memoryMB"];
	        this.cleanedAt = source["cleanedAt"];
	    }
	}
	export class ProcessView {
	    pid: number;
	    threads: number;
	    memoryMB: number;
	    status: string;
	    statusLabel: string;
	    path: string;

	    static createFrom(source: any = {}) {
	        return new ProcessView(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.threads = source["threads"];
	        this.memoryMB = source["memoryMB"];
	        this.status = source["status"];
	        this.statusLabel = source["statusLabel"];
	        this.path = source["path"];
	    }
	}
	export class GuardianStatus {
	    enabled: boolean;
	    state: string;
	    stateLabel: string;
	    lastScan: string;
	    lastAction: string;
	    lastError: string;
	    totalKilled: number;
	    scanInterval: number;
	    processes: ProcessView[];
	    history: CleanupRecord[];

	    static createFrom(source: any = {}) {
	        return new GuardianStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.state = source["state"];
	        this.stateLabel = source["stateLabel"];
	        this.lastScan = source["lastScan"];
	        this.lastAction = source["lastAction"];
	        this.lastError = source["lastError"];
	        this.totalKilled = source["totalKilled"];
	        this.scanInterval = source["scanInterval"];
	        this.processes = this.convertValues(source["processes"], ProcessView);
	        this.history = this.convertValues(source["history"], CleanupRecord);
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
