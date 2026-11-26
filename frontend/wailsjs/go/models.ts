export namespace fsops {
	
	export class ConflictInfo {
	    sameDir: boolean;
	    names: string[];
	
	    static createFrom(source: any = {}) {
	        return new ConflictInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sameDir = source["sameDir"];
	        this.names = source["names"];
	    }
	}
	export class Drive {
	    path: string;
	    label: string;
	    kind: string;
	    total: number;
	    free: number;
	
	    static createFrom(source: any = {}) {
	        return new Drive(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
	        this.kind = source["kind"];
	        this.total = source["total"];
	        this.free = source["free"];
	    }
	}
	export class Entry {
	    name: string;
	    isDir: boolean;
	    size: number;
	    mod: number;
	    hidden: boolean;
	    link: boolean;
	    label?: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.mod = source["mod"];
	        this.hidden = source["hidden"];
	        this.link = source["link"];
	        this.label = source["label"];
	    }
	}
	export class Listing {
	    path: string;
	    parent: string;
	    hasParent: boolean;
	    entries: Entry[];
	
	    static createFrom(source: any = {}) {
	        return new Listing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.parent = source["parent"];
	        this.hasParent = source["hasParent"];
	        this.entries = this.convertValues(source["entries"], Entry);
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
	export class Place {
	    name: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new Place(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	    }
	}
	export class RenameItem {
	    path: string;
	    oldName: string;
	    newName: string;
	    status: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new RenameItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.oldName = source["oldName"];
	        this.newName = source["newName"];
	        this.status = source["status"];
	        this.message = source["message"];
	    }
	}
	export class RenameRule {
	    mode: string;
	    find: string;
	    replace: string;
	    regex: boolean;
	    caseSensitive: boolean;
	    prefix: string;
	    suffix: string;
	    template: string;
	    start: number;
	    step: number;
	    digits: number;
	    caseMode: string;
	    applyToExt: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RenameRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.find = source["find"];
	        this.replace = source["replace"];
	        this.regex = source["regex"];
	        this.caseSensitive = source["caseSensitive"];
	        this.prefix = source["prefix"];
	        this.suffix = source["suffix"];
	        this.template = source["template"];
	        this.start = source["start"];
	        this.step = source["step"];
	        this.digits = source["digits"];
	        this.caseMode = source["caseMode"];
	        this.applyToExt = source["applyToExt"];
	    }
	}
	export class Result {
	    ok: number;
	    skipped: number;
	    failed: string[];
	    moreFailed: number;
	    warnings: string[];
	    cancelled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.skipped = source["skipped"];
	        this.failed = source["failed"];
	        this.moreFailed = source["moreFailed"];
	        this.warnings = source["warnings"];
	        this.cancelled = source["cancelled"];
	    }
	}
	export class Size {
	    bytes: number;
	    files: number;
	    folders: number;
	
	    static createFrom(source: any = {}) {
	        return new Size(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bytes = source["bytes"];
	        this.files = source["files"];
	        this.folders = source["folders"];
	    }
	}

}

export namespace main {
	
	export class BootInfo {
	    home: string;
	    sep: string;
	    os: string;
	    drives: fsops.Drive[];
	    places: fsops.Place[];
	
	    static createFrom(source: any = {}) {
	        return new BootInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.home = source["home"];
	        this.sep = source["sep"];
	        this.os = source["os"];
	        this.drives = this.convertValues(source["drives"], fsops.Drive);
	        this.places = this.convertValues(source["places"], fsops.Place);
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
	export class ClipboardFiles {
	    paths: string[];
	    cut: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ClipboardFiles(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.cut = source["cut"];
	    }
	}
	export class Locations {
	    drives: fsops.Drive[];
	    quick: fsops.Place[];
	    quickFrom: string;
	
	    static createFrom(source: any = {}) {
	        return new Locations(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.drives = this.convertValues(source["drives"], fsops.Drive);
	        this.quick = this.convertValues(source["quick"], fsops.Place);
	        this.quickFrom = source["quickFrom"];
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

