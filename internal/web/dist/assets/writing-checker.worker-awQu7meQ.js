(function(){var e=Object.defineProperty,t=e=>{throw TypeError(e)},n=(t,n,r)=>n in t?e(t,n,{enumerable:!0,configurable:!0,writable:!0,value:r}):t[n]=r,r=(e,t,r)=>n(e,typeof t==`symbol`?t:t+``,r),i=(e,n,r)=>n.has(e)||t(`Cannot `+r),a=(e,t,n)=>(i(e,t,`read from private field`),n?n.call(e):t.get(e)),o=(e,n,r)=>n.has(e)?t(`Cannot add the same private member more than once`):n instanceof WeakSet?n.add(e):n.set(e,r),s=(e,t,n,r)=>(i(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),c,l;let u=Object.freeze({American:0,0:`American`,British:1,1:`British`,Australian:2,2:`Australian`,Canadian:3,3:`Canadian`,Indian:4,4:`Indian`}),d=Object.freeze({Plain:0,0:`Plain`,Markdown:1,1:`Markdown`,Typst:2,2:`Typst`}),f=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,se.register(n,n.__wbg_ptr,n),n}static __unwrap(t){return t instanceof e?t.__destroy_into_raw():0}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,se.unregister(this),e}free(){let e=this.__destroy_into_raw();A.__wbg_lint_free(e,0)}static from_json(t){let n=T(t,A.__wbindgen_malloc,A.__wbindgen_realloc),r=k,i=A.lint_from_json(n,r);if(i[2])throw E(i[1]);return e.__wrap(i[0])}get_problem_text(){let e,t;try{let n=A.lint_get_problem_text(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}lint_kind(){let e,t;try{let n=A.lint_lint_kind(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}lint_kind_pretty(){let e,t;try{let n=A.lint_lint_kind_pretty(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}message(){let e,t;try{let n=A.lint_message(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}message_html(){let e,t;try{let n=A.lint_message_html(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}span(){let e=A.lint_span(this.__wbg_ptr);return m.__wrap(e)}suggestion_count(){return A.lint_suggestion_count(this.__wbg_ptr)>>>0}suggestions(){let e=A.lint_suggestions(this.__wbg_ptr);var t=g(e[0],e[1]);return A.__wbindgen_free(e[0],e[1]*4,4),t}to_json(){let e,t;try{let n=A.lint_to_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(f.prototype[Symbol.dispose]=f.prototype.free);let ee=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,ce.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,ce.unregister(this),e}free(){let e=this.__destroy_into_raw();A.__wbg_linter_free(e,0)}apply_suggestion(e,t,n){let r,i;try{let s=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),c=k;pe(t,f),pe(n,h);let l=A.linter_apply_suggestion(this.__wbg_ptr,s,c,t.__wbg_ptr,n.__wbg_ptr);var a=l[0],o=l[1];if(l[3])throw a=0,o=0,E(l[2]);return r=a,i=o,b(a,o)}finally{A.__wbindgen_free(r,i,1)}}clear_ignored_lints(){A.linter_clear_ignored_lints(this.__wbg_ptr)}clear_words(){A.linter_clear_words(this.__wbg_ptr)}context_hash(e,t){let n=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),r=k;pe(t,f);let i=A.linter_context_hash(this.__wbg_ptr,n,r,t.__wbg_ptr);return BigInt.asUintN(64,i)}export_ignored_lints(){let e,t;try{let n=A.linter_export_ignored_lints(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}export_words(){let e=A.linter_export_words(this.__wbg_ptr);var t=g(e[0],e[1]);return A.__wbindgen_free(e[0],e[1]*4,4),t}generate_stats_file(){let e,t;try{let n=A.linter_generate_stats_file(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get_dialect(){return A.linter_get_dialect(this.__wbg_ptr)}get_lint_config_as_json(){let e,t;try{let n=A.linter_get_lint_config_as_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get_lint_config_as_object(){return A.linter_get_lint_config_as_object(this.__wbg_ptr)}get_lint_descriptions_as_json(){let e,t;try{let n=A.linter_get_lint_descriptions_as_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get_lint_descriptions_as_object(){return A.linter_get_lint_descriptions_as_object(this.__wbg_ptr)}get_lint_descriptions_html_as_json(){let e,t;try{let n=A.linter_get_lint_descriptions_html_as_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get_lint_descriptions_html_as_object(){return A.linter_get_lint_descriptions_html_as_object(this.__wbg_ptr)}get_structured_lint_config_as_json(){let e,t;try{let n=A.linter_get_structured_lint_config_as_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get_structured_lint_config_as_object(){return A.linter_get_structured_lint_config_as_object(this.__wbg_ptr)}ignore_hashes(e){let t=_e(e,A.__wbindgen_malloc),n=k;A.linter_ignore_hashes(this.__wbg_ptr,t,n)}ignore_lints(e,t){let n=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),r=k,i=ye(t,A.__wbindgen_malloc),a=k;A.linter_ignore_lints(this.__wbg_ptr,n,r,i,a)}import_ignored_lints(e){let t=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),n=k,r=A.linter_import_ignored_lints(this.__wbg_ptr,t,n);if(r[1])throw E(r[0])}import_stats_file(e){let t=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),n=k,r=A.linter_import_stats_file(this.__wbg_ptr,t,n);if(r[1])throw E(r[0])}import_weirpack(e){let t=ve(e,A.__wbindgen_malloc),n=k,r=A.linter_import_weirpack(this.__wbg_ptr,t,n);if(r[2])throw E(r[1]);return E(r[0])}import_words(e){let t=ye(e,A.__wbindgen_malloc),n=k;A.linter_import_words(this.__wbg_ptr,t,n)}is_likely_english(e){let t=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),n=k;return A.linter_is_likely_english(this.__wbg_ptr,t,n)!==0}isolate_english(e){let t,n;try{let r=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),i=k,a=A.linter_isolate_english(this.__wbg_ptr,r,i);return t=a[0],n=a[1],b(a[0],a[1])}finally{A.__wbindgen_free(t,n,1)}}lint(e,t,n,r,i,a){let o=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),s=k;var c=w(r)?0:T(r,A.__wbindgen_malloc,A.__wbindgen_realloc),l=k;let u=A.linter_lint(this.__wbg_ptr,o,s,t,n,c,l,i,a);var d=g(u[0],u[1]);return A.__wbindgen_free(u[0],u[1]*4,4),d}static new(t){let n=A.linter_new(t);return e.__wrap(n)}organized_lints(e,t,n,r,i,a){let o=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),s=k;var c=w(r)?0:T(r,A.__wbindgen_malloc,A.__wbindgen_realloc),l=k;let u=A.linter_organized_lints(this.__wbg_ptr,o,s,t,n,c,l,i,a);var d=g(u[0],u[1]);return A.__wbindgen_free(u[0],u[1]*4,4),d}set_lint_config_from_json(e){let t=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),n=k,r=A.linter_set_lint_config_from_json(this.__wbg_ptr,t,n);if(r[1])throw E(r[0])}set_lint_config_from_object(e){let t=A.linter_set_lint_config_from_object(this.__wbg_ptr,e);if(t[1])throw E(t[0])}summarize_stats(e,t){return A.linter_summarize_stats(this.__wbg_ptr,!w(e),w(e)?BigInt(0):e,!w(t),w(t)?BigInt(0):t)}};Symbol.dispose&&(ee.prototype[Symbol.dispose]=ee.prototype.free);let p=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,le.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,le.unregister(this),e}free(){let e=this.__destroy_into_raw();A.__wbg_organizedgroup_free(e,0)}get group(){let e,t;try{let n=A.__wbg_get_organizedgroup_group(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}get lints(){let e=A.__wbg_get_organizedgroup_lints(this.__wbg_ptr);var t=g(e[0],e[1]);return A.__wbindgen_free(e[0],e[1]*4,4),t}set group(e){let t=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),n=k;A.__wbg_set_organizedgroup_group(this.__wbg_ptr,t,n)}set lints(e){let t=ye(e,A.__wbindgen_malloc),n=k;A.__wbg_set_organizedgroup_lints(this.__wbg_ptr,t,n)}};Symbol.dispose&&(p.prototype[Symbol.dispose]=p.prototype.free);let m=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,ue.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,ue.unregister(this),e}free(){let e=this.__destroy_into_raw();A.__wbg_span_free(e,0)}get end(){return A.__wbg_get_span_end(this.__wbg_ptr)>>>0}get start(){return A.__wbg_get_span_start(this.__wbg_ptr)>>>0}set end(e){A.__wbg_set_span_end(this.__wbg_ptr,e)}set start(e){A.__wbg_set_span_start(this.__wbg_ptr,e)}static from_json(t){let n=T(t,A.__wbindgen_malloc,A.__wbindgen_realloc),r=k,i=A.span_from_json(n,r);if(i[2])throw E(i[1]);return e.__wrap(i[0])}is_empty(){return A.span_is_empty(this.__wbg_ptr)!==0}len(){return A.span_len(this.__wbg_ptr)>>>0}static new(t,n){let r=A.span_new(t,n);return e.__wrap(r)}to_json(){let e,t;try{let n=A.span_to_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(m.prototype[Symbol.dispose]=m.prototype.free);let h=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,de.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,de.unregister(this),e}free(){let e=this.__destroy_into_raw();A.__wbg_suggestion_free(e,0)}static from_json(t){let n=T(t,A.__wbindgen_malloc,A.__wbindgen_realloc),r=k,i=A.suggestion_from_json(n,r);if(i[2])throw E(i[1]);return e.__wrap(i[0])}get_replacement_text(){let e,t;try{let n=A.suggestion_get_replacement_text(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}kind(){return A.suggestion_kind(this.__wbg_ptr)}to_json(){let e,t;try{let n=A.suggestion_to_json(this.__wbg_ptr);return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(h.prototype[Symbol.dispose]=h.prototype.free);let te=Object.freeze({Replace:0,0:`Replace`,Remove:1,1:`Remove`,InsertAfter:2,2:`InsertAfter`});function ne(){return A.get_default_lint_config()}function re(){let e,t;try{let n=A.get_default_lint_config_as_json();return e=n[0],t=n[1],b(n[0],n[1])}finally{A.__wbindgen_free(e,t,1)}}function ie(){A.setup()}function ae(e){let t,n;try{let r=T(e,A.__wbindgen_malloc,A.__wbindgen_realloc),i=k,a=A.to_title_case(r,i);return t=a[0],n=a[1],b(a[0],a[1])}finally{A.__wbindgen_free(t,n,1)}}function oe(){return{__proto__:null,"./harper_wasm_slim_bg.js":{__proto__:null,__wbg_Error_408e67f47ca7b58b:function(e,t){return Error(b(e,t))},__wbg_String_8564e559799eccda:function(e,t){let n=T(String(t),A.__wbindgen_malloc,A.__wbindgen_realloc),r=k;y().setInt32(e+4,r,!0),y().setInt32(e+0,n,!0)},__wbg___wbindgen_boolean_get_c9c83ebd41b34df3:function(e){let t=e,n=typeof t==`boolean`?t:void 0;return w(n)?16777215:+!!n},__wbg___wbindgen_debug_string_a57024b9c6e4a48b:function(e,t){let n=T(me(t),A.__wbindgen_malloc,A.__wbindgen_realloc),r=k;y().setInt32(e+4,r,!0),y().setInt32(e+0,n,!0)},__wbg___wbindgen_is_function_5e4570eb24ffa122:function(e){return typeof e==`function`},__wbg___wbindgen_is_object_a2790eb24c211ea0:function(e){let t=e;return typeof t==`object`&&!!t},__wbg___wbindgen_is_string_e6f02f0ea5f20a32:function(e){return typeof e==`string`},__wbg___wbindgen_jsval_loose_eq_acf2776254a8d832:function(e,t){return e==t},__wbg___wbindgen_number_get_136b9679cab35cfb:function(e,t){let n=t,r=typeof n==`number`?n:void 0;y().setFloat64(e+8,w(r)?0:r,!0),y().setInt32(e+0,!w(r),!0)},__wbg___wbindgen_string_get_d154f1e671052120:function(e,t){let n=t,r=typeof n==`string`?n:void 0;var i=w(r)?0:T(r,A.__wbindgen_malloc,A.__wbindgen_realloc),a=k;y().setInt32(e+4,a,!0),y().setInt32(e+0,i,!0)},__wbg___wbindgen_throw_bb96b2010945f0bc:function(e,t){throw Error(b(e,t))},__wbg_call_1c5886ab9c57d1c7:function(){return C(function(e,t){return e.call(t)},arguments)},__wbg_done_669171204c3dcae2:function(e){return e.done},__wbg_entries_7774d489e1da5f4f:function(e){return Object.entries(e)},__wbg_error_757e9472f8410341:function(e,t){let n,r;try{n=e,r=t,console.error(b(e,t))}finally{A.__wbindgen_free(n,r,1)}},__wbg_getRandomValues_a608c4436c19407a:function(){return C(function(e,t){globalThis.crypto.getRandomValues(he(e,t))},arguments)},__wbg_getRandomValues_e446ea5ffdd14ee5:function(){return C(function(e,t){globalThis.crypto.getRandomValues(he(e,t))},arguments)},__wbg_getTime_63fb0332e6c4ec17:function(e){return e.getTime()},__wbg_get_c0c8f8d7da0c03dd:function(e,t){return e[t>>>0]},__wbg_get_d173c0308df22d37:function(){return C(function(e,t){return Reflect.get(e,t)},arguments)},__wbg_get_unchecked_e20b893aeafc3fca:function(e,t){return e[t>>>0]},__wbg_instanceof_ArrayBuffer_993d02d2d254cad1:function(e){let t;try{t=e instanceof ArrayBuffer}catch{t=!1}return t},__wbg_instanceof_Uint8Array_f935dbb0aa7cdeed:function(e){let t;try{t=e instanceof Uint8Array}catch{t=!1}return t},__wbg_iterator_5cebbb86e33c6dd6:function(){return Symbol.iterator},__wbg_length_36bd29c6848c2144:function(e){return e.length},__wbg_length_ecfa2c63d3d0d82c:function(e){return e.length},__wbg_lint_new:function(e){return f.__wrap(e)},__wbg_lint_unwrap:function(e){return f.__unwrap(e)},__wbg_log_1f8cbb01c83d06c2:function(e,t,n,r,i,a,o,s){let c,l;try{c=e,l=t,console.log(b(e,t),b(n,r),b(i,a),b(o,s))}finally{A.__wbindgen_free(c,l,1)}},__wbg_log_a54ca6b45e09078a:function(e,t){let n,r;try{n=e,r=t,console.log(b(e,t))}finally{A.__wbindgen_free(n,r,1)}},__wbg_mark_6b7f03786f5e4d61:function(e,t){performance.mark(b(e,t))},__wbg_measure_0e21b33a1c6e3a29:function(){return C(function(e,t,n,r){let i,a,o,s;try{i=e,a=t,o=n,s=r,performance.measure(b(e,t),b(n,r))}finally{A.__wbindgen_free(i,a,1),A.__wbindgen_free(o,s,1)}},arguments)},__wbg_new_0_f117d868b403dc07:function(){return/* @__PURE__ */ new Date},__wbg_new_116be93542d39019:function(){return[]},__wbg_new_227d7c05414eb861:function(){return/* @__PURE__ */ Error()},__wbg_new_77cc4f4f472aeb81:function(e){return new Uint8Array(e)},__wbg_new_cdf041679ded4c5f:function(){return/* @__PURE__ */ new Map},__wbg_new_ebe3e0f6837f0879:function(){return{}},__wbg_next_42cf16ee0dafc9e2:function(){return C(function(e){return e.next()},arguments)},__wbg_next_8f26b64fa5e9f64b:function(e){return e.next},__wbg_organizedgroup_new:function(e){return p.__wrap(e)},__wbg_prototypesetcall_de8e0d9553586985:function(e,t,n){Uint8Array.prototype.set.call(he(e,t),n)},__wbg_set_014226dfeca53178:function(e,t,n){return e.set(t,n)},__wbg_set_6be42768c690e380:function(e,t,n){e[t]=n},__wbg_set_a80955eb93b145c6:function(e,t,n){e[t>>>0]=n},__wbg_stack_3b0d974bbf31e44f:function(e,t){let n=t.stack,r=T(n,A.__wbindgen_malloc,A.__wbindgen_realloc),i=k;y().setInt32(e+4,i,!0),y().setInt32(e+0,r,!0)},__wbg_suggestion_new:function(e){return h.__wrap(e)},__wbg_value_1e2369fab29b420e:function(e){return e.value},__wbindgen_cast_0000000000000001:function(e){return e},__wbindgen_cast_0000000000000002:function(e,t){return b(e,t)},__wbindgen_init_externref_table:function(){let e=A.__wbindgen_externrefs,t=e.grow(4);e.set(0,void 0),e.set(t+0,void 0),e.set(t+1,null),e.set(t+2,!0),e.set(t+3,!1)}}}}let se=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>A.__wbg_lint_free(e,1)),ce=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>A.__wbg_linter_free(e,1)),le=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>A.__wbg_organizedgroup_free(e,1)),ue=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>A.__wbg_span_free(e,1)),de=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>A.__wbg_suggestion_free(e,1));function fe(e){let t=A.__externref_table_alloc();return A.__wbindgen_externrefs.set(t,e),t}function pe(e,t){if(!(e instanceof t))throw Error(`expected instance of ${t.name}`)}function me(e){let t=typeof e;if(t==`number`||t==`boolean`||e==null)return`${e}`;if(t==`string`)return`"${e}"`;if(t==`symbol`){let t=e.description;return t==null?`Symbol`:`Symbol(${t})`}if(t==`function`){let t=e.name;return typeof t==`string`&&t.length>0?`Function(${t})`:`Function`}if(Array.isArray(e)){let t=e.length,n=`[`;t>0&&(n+=me(e[0]));for(let r=1;r<t;r++)n+=`, `+me(e[r]);return n+=`]`,n}let n=/\[object ([^\]]+)\]/.exec(toString.call(e)),r;if(n&&n.length>1)r=n[1];else return toString.call(e);if(r==`Object`)try{return`Object(`+JSON.stringify(e)+`)`}catch{return`Object`}return e instanceof Error?`${e.name}: ${e.message}
${e.stack}`:r}function g(e,t){e>>>=0;let n=y(),r=[];for(let i=e;i<e+4*t;i+=4)r.push(A.__wbindgen_externrefs.get(n.getUint32(i,!0)));return A.__externref_drop_slice(e,t),r}function he(e,t){return e>>>=0,S().subarray(e/1,e/1+t)}let _=null;function ge(){return(_===null||_.byteLength===0)&&(_=new BigUint64Array(A.memory.buffer)),_}let v=null;function y(){return(v===null||v.buffer.detached===!0||v.buffer.detached===void 0&&v.buffer!==A.memory.buffer)&&(v=new DataView(A.memory.buffer)),v}function b(e,t){return xe(e>>>0,t)}let x=null;function S(){return(x===null||x.byteLength===0)&&(x=new Uint8Array(A.memory.buffer)),x}function C(e,t){try{return e.apply(this,t)}catch(e){let t=fe(e);A.__wbindgen_exn_store(t)}}function w(e){return e==null}function _e(e,t){let n=t(e.length*8,8)>>>0;return ge().set(e,n/8),k=e.length,n}function ve(e,t){let n=t(e.length*1,1)>>>0;return S().set(e,n/1),k=e.length,n}function ye(e,t){let n=t(e.length*4,4)>>>0;for(let t=0;t<e.length;t++){let r=fe(e[t]);y().setUint32(n+4*t,r,!0)}return k=e.length,n}function T(e,t,n){if(n===void 0){let n=O.encode(e),r=t(n.length,1)>>>0;return S().subarray(r,r+n.length).set(n),k=n.length,r}let r=e.length,i=t(r,1)>>>0,a=S(),o=0;for(;o<r;o++){let t=e.charCodeAt(o);if(t>127)break;a[i+o]=t}if(o!==r){o!==0&&(e=e.slice(o)),i=n(i,r,r=o+e.length*3,1)>>>0;let t=S().subarray(i+o,i+r),a=O.encodeInto(e,t);o+=a.written,i=n(i,r,o,1)>>>0}return k=o,i}function E(e){let t=A.__wbindgen_externrefs.get(e);return A.__externref_table_dealloc(e),t}let D=new TextDecoder(`utf-8`,{ignoreBOM:!0,fatal:!0});D.decode();let be=0;function xe(e,t){return be+=t,be>=2146435072&&(D=new TextDecoder(`utf-8`,{ignoreBOM:!0,fatal:!0}),D.decode(),be=t),D.decode(S().subarray(e,e+t))}let O=new TextEncoder;`encodeInto`in O||(O.encodeInto=function(e,t){let n=O.encode(e);return t.set(n),{read:e.length,written:n.length}});let k=0,A;function Se(e,t){return A=e.exports,_=null,v=null,x=null,A.__wbindgen_start(),A}async function Ce(e,t){if(typeof Response==`function`&&e instanceof Response){if(!e.ok)throw Error(`failed to fetch Wasm: ${e.status} ${e.statusText} fetching '${e.url}'`);if(typeof WebAssembly.instantiateStreaming==`function`)try{return await WebAssembly.instantiateStreaming(e,t)}catch(t){if(n(e.type)&&e.headers.get(`Content-Type`)!==`application/wasm`)console.warn("`WebAssembly.instantiateStreaming` failed because your server does not serve Wasm with `application/wasm` MIME type. Falling back to `WebAssembly.instantiate` which is slower. Original error:\n",t);else throw t}let r=await e.arrayBuffer();return await WebAssembly.instantiate(r,t)}{let n=await WebAssembly.instantiate(e,t);return n instanceof WebAssembly.Instance?{instance:n,module:e}:n}function n(e){switch(e){case`basic`:case`cors`:case`default`:return!0}return!1}}function we(e){if(A!==void 0)return A;e!==void 0&&(Object.getPrototypeOf(e)===Object.prototype?{module:e}=e:console.warn("using deprecated parameters for `initSync()`; pass a single object instead"));let t=oe();return e instanceof WebAssembly.Module||(e=new WebAssembly.Module(e)),Se(new WebAssembly.Instance(e,t))}async function Te(e){if(A!==void 0)return A;e!==void 0&&(Object.getPrototypeOf(e)===Object.prototype?{module_or_path:e}=e:console.warn(`using deprecated parameters for the initialization function; pass a single object instead`)),e===void 0&&(e=new URL);let t=oe();(typeof e==`string`||typeof Request==`function`&&e instanceof Request||typeof URL==`function`&&e instanceof URL)&&(e=fetch(e));let{instance:n}=await Ce(await e,t);return Se(n)}let Ee=/* @__PURE__ */ Object.freeze(/* @__PURE__ */ Object.defineProperty({__proto__:null,Dialect:u,Language:d,Lint:f,Linter:ee,OrganizedGroup:p,Span:m,Suggestion:h,SuggestionKind:te,default:Te,get_default_lint_config:ne,get_default_lint_config_as_json:re,initSync:we,setup:ie,to_title_case:ae},Symbol.toStringTag,{value:`Module`})),De=Object.freeze({American:0,0:`American`,British:1,1:`British`,Australian:2,2:`Australian`,Canadian:3,3:`Canadian`,Indian:4,4:`Indian`}),Oe=Object.freeze({Plain:0,0:`Plain`,Markdown:1,1:`Markdown`,Typst:2,2:`Typst`});var j=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,Ie.register(n,n.__wbg_ptr,n),n}static __unwrap(t){return t instanceof e?t.__destroy_into_raw():0}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,Ie.unregister(this),e}free(){let e=this.__destroy_into_raw();Y.__wbg_lint_free(e,0)}static from_json(t){let n=W(t,Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J,i=Y.lint_from_json(n,r);if(i[2])throw G(i[1]);return e.__wrap(i[0])}get_problem_text(){let e,t;try{let n=Y.lint_get_problem_text(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}lint_kind(){let e,t;try{let n=Y.lint_lint_kind(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}lint_kind_pretty(){let e,t;try{let n=Y.lint_lint_kind_pretty(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}message(){let e,t;try{let n=Y.lint_message(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}message_html(){let e,t;try{let n=Y.lint_message_html(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}span(){let e=Y.lint_span(this.__wbg_ptr);return N.__wrap(e)}suggestion_count(){return Y.lint_suggestion_count(this.__wbg_ptr)>>>0}suggestions(){let e=Y.lint_suggestions(this.__wbg_ptr);var t=F(e[0],e[1]);return Y.__wbindgen_free(e[0],e[1]*4,4),t}to_json(){let e,t;try{let n=Y.lint_to_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(j.prototype[Symbol.dispose]=j.prototype.free);var ke=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,Le.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,Le.unregister(this),e}free(){let e=this.__destroy_into_raw();Y.__wbg_linter_free(e,0)}apply_suggestion(e,t,n){let r,i;try{let s=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),c=J;He(t,j),He(n,P);let l=Y.linter_apply_suggestion(this.__wbg_ptr,s,c,t.__wbg_ptr,n.__wbg_ptr);var a=l[0],o=l[1];if(l[3])throw a=0,o=0,G(l[2]);return r=a,i=o,z(a,o)}finally{Y.__wbindgen_free(r,i,1)}}clear_ignored_lints(){Y.linter_clear_ignored_lints(this.__wbg_ptr)}clear_words(){Y.linter_clear_words(this.__wbg_ptr)}context_hash(e,t){let n=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J;He(t,j);let i=Y.linter_context_hash(this.__wbg_ptr,n,r,t.__wbg_ptr);return BigInt.asUintN(64,i)}export_ignored_lints(){let e,t;try{let n=Y.linter_export_ignored_lints(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}export_words(){let e=Y.linter_export_words(this.__wbg_ptr);var t=F(e[0],e[1]);return Y.__wbindgen_free(e[0],e[1]*4,4),t}generate_stats_file(){let e,t;try{let n=Y.linter_generate_stats_file(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get_dialect(){return Y.linter_get_dialect(this.__wbg_ptr)}get_lint_config_as_json(){let e,t;try{let n=Y.linter_get_lint_config_as_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get_lint_config_as_object(){return Y.linter_get_lint_config_as_object(this.__wbg_ptr)}get_lint_descriptions_as_json(){let e,t;try{let n=Y.linter_get_lint_descriptions_as_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get_lint_descriptions_as_object(){return Y.linter_get_lint_descriptions_as_object(this.__wbg_ptr)}get_lint_descriptions_html_as_json(){let e,t;try{let n=Y.linter_get_lint_descriptions_html_as_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get_lint_descriptions_html_as_object(){return Y.linter_get_lint_descriptions_html_as_object(this.__wbg_ptr)}get_structured_lint_config_as_json(){let e,t;try{let n=Y.linter_get_structured_lint_config_as_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get_structured_lint_config_as_object(){return Y.linter_get_structured_lint_config_as_object(this.__wbg_ptr)}ignore_hashes(e){let t=Ke(e,Y.__wbindgen_malloc),n=J;Y.linter_ignore_hashes(this.__wbg_ptr,t,n)}ignore_lints(e,t){let n=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J,i=Je(t,Y.__wbindgen_malloc),a=J;Y.linter_ignore_lints(this.__wbg_ptr,n,r,i,a)}import_ignored_lints(e){let t=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),n=J,r=Y.linter_import_ignored_lints(this.__wbg_ptr,t,n);if(r[1])throw G(r[0])}import_stats_file(e){let t=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),n=J,r=Y.linter_import_stats_file(this.__wbg_ptr,t,n);if(r[1])throw G(r[0])}import_weirpack(e){let t=qe(e,Y.__wbindgen_malloc),n=J,r=Y.linter_import_weirpack(this.__wbg_ptr,t,n);if(r[2])throw G(r[1]);return G(r[0])}import_words(e){let t=Je(e,Y.__wbindgen_malloc),n=J;Y.linter_import_words(this.__wbg_ptr,t,n)}is_likely_english(e){let t=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),n=J;return Y.linter_is_likely_english(this.__wbg_ptr,t,n)!==0}isolate_english(e){let t,n;try{let r=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),i=J,a=Y.linter_isolate_english(this.__wbg_ptr,r,i);return t=a[0],n=a[1],z(a[0],a[1])}finally{Y.__wbindgen_free(t,n,1)}}lint(e,t,n,r,i,a){let o=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),s=J;var c=U(r)?0:W(r,Y.__wbindgen_malloc,Y.__wbindgen_realloc),l=J;let u=Y.linter_lint(this.__wbg_ptr,o,s,t,n,c,l,i,a);var d=F(u[0],u[1]);return Y.__wbindgen_free(u[0],u[1]*4,4),d}static new(t){let n=Y.linter_new(t);return e.__wrap(n)}organized_lints(e,t,n,r,i,a){let o=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),s=J;var c=U(r)?0:W(r,Y.__wbindgen_malloc,Y.__wbindgen_realloc),l=J;let u=Y.linter_organized_lints(this.__wbg_ptr,o,s,t,n,c,l,i,a);var d=F(u[0],u[1]);return Y.__wbindgen_free(u[0],u[1]*4,4),d}set_lint_config_from_json(e){let t=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),n=J,r=Y.linter_set_lint_config_from_json(this.__wbg_ptr,t,n);if(r[1])throw G(r[0])}set_lint_config_from_object(e){let t=Y.linter_set_lint_config_from_object(this.__wbg_ptr,e);if(t[1])throw G(t[0])}summarize_stats(e,t){return Y.linter_summarize_stats(this.__wbg_ptr,!U(e),U(e)?BigInt(0):e,!U(t),U(t)?BigInt(0):t)}};Symbol.dispose&&(ke.prototype[Symbol.dispose]=ke.prototype.free);var M=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,Re.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,Re.unregister(this),e}free(){let e=this.__destroy_into_raw();Y.__wbg_organizedgroup_free(e,0)}get group(){let e,t;try{let n=Y.__wbg_get_organizedgroup_group(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}get lints(){let e=Y.__wbg_get_organizedgroup_lints(this.__wbg_ptr);var t=F(e[0],e[1]);return Y.__wbindgen_free(e[0],e[1]*4,4),t}set group(e){let t=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),n=J;Y.__wbg_set_organizedgroup_group(this.__wbg_ptr,t,n)}set lints(e){let t=Je(e,Y.__wbindgen_malloc),n=J;Y.__wbg_set_organizedgroup_lints(this.__wbg_ptr,t,n)}};Symbol.dispose&&(M.prototype[Symbol.dispose]=M.prototype.free);var N=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,ze.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,ze.unregister(this),e}free(){let e=this.__destroy_into_raw();Y.__wbg_span_free(e,0)}get end(){return Y.__wbg_get_span_end(this.__wbg_ptr)>>>0}get start(){return Y.__wbg_get_span_start(this.__wbg_ptr)>>>0}set end(e){Y.__wbg_set_span_end(this.__wbg_ptr,e)}set start(e){Y.__wbg_set_span_start(this.__wbg_ptr,e)}static from_json(t){let n=W(t,Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J,i=Y.span_from_json(n,r);if(i[2])throw G(i[1]);return e.__wrap(i[0])}is_empty(){return Y.span_is_empty(this.__wbg_ptr)!==0}len(){return Y.span_len(this.__wbg_ptr)>>>0}static new(t,n){let r=Y.span_new(t,n);return e.__wrap(r)}to_json(){let e,t;try{let n=Y.span_to_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(N.prototype[Symbol.dispose]=N.prototype.free);var P=class e{static __wrap(t){let n=Object.create(e.prototype);return n.__wbg_ptr=t,Be.register(n,n.__wbg_ptr,n),n}__destroy_into_raw(){let e=this.__wbg_ptr;return this.__wbg_ptr=0,Be.unregister(this),e}free(){let e=this.__destroy_into_raw();Y.__wbg_suggestion_free(e,0)}static from_json(t){let n=W(t,Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J,i=Y.suggestion_from_json(n,r);if(i[2])throw G(i[1]);return e.__wrap(i[0])}get_replacement_text(){let e,t;try{let n=Y.suggestion_get_replacement_text(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}kind(){return Y.suggestion_kind(this.__wbg_ptr)}to_json(){let e,t;try{let n=Y.suggestion_to_json(this.__wbg_ptr);return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}};Symbol.dispose&&(P.prototype[Symbol.dispose]=P.prototype.free);let Ae=Object.freeze({Replace:0,0:`Replace`,Remove:1,1:`Remove`,InsertAfter:2,2:`InsertAfter`});function je(){return Y.get_default_lint_config()}function Me(){let e,t;try{let n=Y.get_default_lint_config_as_json();return e=n[0],t=n[1],z(n[0],n[1])}finally{Y.__wbindgen_free(e,t,1)}}function Ne(){Y.setup()}function Pe(e){let t,n;try{let r=W(e,Y.__wbindgen_malloc,Y.__wbindgen_realloc),i=J,a=Y.to_title_case(r,i);return t=a[0],n=a[1],z(a[0],a[1])}finally{Y.__wbindgen_free(t,n,1)}}function Fe(){return{__proto__:null,"./harper_wasm_bg.js":{__proto__:null,__wbg_Error_408e67f47ca7b58b:function(e,t){return Error(z(e,t))},__wbg_String_8564e559799eccda:function(e,t){let n=W(String(t),Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J;R().setInt32(e+4,r,!0),R().setInt32(e+0,n,!0)},__wbg___wbindgen_boolean_get_c9c83ebd41b34df3:function(e){let t=e,n=typeof t==`boolean`?t:void 0;return U(n)?16777215:+!!n},__wbg___wbindgen_debug_string_a57024b9c6e4a48b:function(e,t){let n=W(Ue(t),Y.__wbindgen_malloc,Y.__wbindgen_realloc),r=J;R().setInt32(e+4,r,!0),R().setInt32(e+0,n,!0)},__wbg___wbindgen_is_function_5e4570eb24ffa122:function(e){return typeof e==`function`},__wbg___wbindgen_is_object_a2790eb24c211ea0:function(e){let t=e;return typeof t==`object`&&!!t},__wbg___wbindgen_is_string_e6f02f0ea5f20a32:function(e){return typeof e==`string`},__wbg___wbindgen_jsval_loose_eq_acf2776254a8d832:function(e,t){return e==t},__wbg___wbindgen_number_get_136b9679cab35cfb:function(e,t){let n=t,r=typeof n==`number`?n:void 0;R().setFloat64(e+8,U(r)?0:r,!0),R().setInt32(e+0,!U(r),!0)},__wbg___wbindgen_string_get_d154f1e671052120:function(e,t){let n=t,r=typeof n==`string`?n:void 0;var i=U(r)?0:W(r,Y.__wbindgen_malloc,Y.__wbindgen_realloc),a=J;R().setInt32(e+4,a,!0),R().setInt32(e+0,i,!0)},__wbg___wbindgen_throw_bb96b2010945f0bc:function(e,t){throw Error(z(e,t))},__wbg_call_1c5886ab9c57d1c7:function(){return H(function(e,t){return e.call(t)},arguments)},__wbg_done_669171204c3dcae2:function(e){return e.done},__wbg_entries_7774d489e1da5f4f:function(e){return Object.entries(e)},__wbg_error_757e9472f8410341:function(e,t){let n,r;try{n=e,r=t,console.error(z(e,t))}finally{Y.__wbindgen_free(n,r,1)}},__wbg_getRandomValues_a608c4436c19407a:function(){return H(function(e,t){globalThis.crypto.getRandomValues(We(e,t))},arguments)},__wbg_getRandomValues_e446ea5ffdd14ee5:function(){return H(function(e,t){globalThis.crypto.getRandomValues(We(e,t))},arguments)},__wbg_getTime_63fb0332e6c4ec17:function(e){return e.getTime()},__wbg_get_c0c8f8d7da0c03dd:function(e,t){return e[t>>>0]},__wbg_get_d173c0308df22d37:function(){return H(function(e,t){return Reflect.get(e,t)},arguments)},__wbg_get_unchecked_e20b893aeafc3fca:function(e,t){return e[t>>>0]},__wbg_instanceof_ArrayBuffer_993d02d2d254cad1:function(e){let t;try{t=e instanceof ArrayBuffer}catch{t=!1}return t},__wbg_instanceof_Uint8Array_f935dbb0aa7cdeed:function(e){let t;try{t=e instanceof Uint8Array}catch{t=!1}return t},__wbg_iterator_5cebbb86e33c6dd6:function(){return Symbol.iterator},__wbg_length_36bd29c6848c2144:function(e){return e.length},__wbg_length_ecfa2c63d3d0d82c:function(e){return e.length},__wbg_lint_new:function(e){return j.__wrap(e)},__wbg_lint_unwrap:function(e){return j.__unwrap(e)},__wbg_log_1f8cbb01c83d06c2:function(e,t,n,r,i,a,o,s){let c,l;try{c=e,l=t,console.log(z(e,t),z(n,r),z(i,a),z(o,s))}finally{Y.__wbindgen_free(c,l,1)}},__wbg_log_a54ca6b45e09078a:function(e,t){let n,r;try{n=e,r=t,console.log(z(e,t))}finally{Y.__wbindgen_free(n,r,1)}},__wbg_mark_6b7f03786f5e4d61:function(e,t){performance.mark(z(e,t))},__wbg_measure_0e21b33a1c6e3a29:function(){return H(function(e,t,n,r){let i,a,o,s;try{i=e,a=t,o=n,s=r,performance.measure(z(e,t),z(n,r))}finally{Y.__wbindgen_free(i,a,1),Y.__wbindgen_free(o,s,1)}},arguments)},__wbg_new_0_f117d868b403dc07:function(){return/* @__PURE__ */ new Date},__wbg_new_116be93542d39019:function(){return[]},__wbg_new_227d7c05414eb861:function(){return/* @__PURE__ */ Error()},__wbg_new_77cc4f4f472aeb81:function(e){return new Uint8Array(e)},__wbg_new_cdf041679ded4c5f:function(){return/* @__PURE__ */ new Map},__wbg_new_ebe3e0f6837f0879:function(){return{}},__wbg_next_42cf16ee0dafc9e2:function(){return H(function(e){return e.next()},arguments)},__wbg_next_8f26b64fa5e9f64b:function(e){return e.next},__wbg_organizedgroup_new:function(e){return M.__wrap(e)},__wbg_prototypesetcall_de8e0d9553586985:function(e,t,n){Uint8Array.prototype.set.call(We(e,t),n)},__wbg_set_014226dfeca53178:function(e,t,n){return e.set(t,n)},__wbg_set_6be42768c690e380:function(e,t,n){e[t]=n},__wbg_set_a80955eb93b145c6:function(e,t,n){e[t>>>0]=n},__wbg_stack_3b0d974bbf31e44f:function(e,t){let n=t.stack,r=W(n,Y.__wbindgen_malloc,Y.__wbindgen_realloc),i=J;R().setInt32(e+4,i,!0),R().setInt32(e+0,r,!0)},__wbg_suggestion_new:function(e){return P.__wrap(e)},__wbg_value_1e2369fab29b420e:function(e){return e.value},__wbindgen_cast_0000000000000001:function(e){return e},__wbindgen_cast_0000000000000002:function(e,t){return z(e,t)},__wbindgen_init_externref_table:function(){let e=Y.__wbindgen_externrefs,t=e.grow(4);e.set(0,void 0),e.set(t+0,void 0),e.set(t+1,null),e.set(t+2,!0),e.set(t+3,!1)}}}}let Ie=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>Y.__wbg_lint_free(e,1)),Le=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>Y.__wbg_linter_free(e,1)),Re=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>Y.__wbg_organizedgroup_free(e,1)),ze=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>Y.__wbg_span_free(e,1)),Be=typeof FinalizationRegistry>`u`?{register:()=>{},unregister:()=>{}}:new FinalizationRegistry(e=>Y.__wbg_suggestion_free(e,1));function Ve(e){let t=Y.__externref_table_alloc();return Y.__wbindgen_externrefs.set(t,e),t}function He(e,t){if(!(e instanceof t))throw Error(`expected instance of ${t.name}`)}function Ue(e){let t=typeof e;if(t==`number`||t==`boolean`||e==null)return`${e}`;if(t==`string`)return`"${e}"`;if(t==`symbol`){let t=e.description;return t==null?`Symbol`:`Symbol(${t})`}if(t==`function`){let t=e.name;return typeof t==`string`&&t.length>0?`Function(${t})`:`Function`}if(Array.isArray(e)){let t=e.length,n=`[`;t>0&&(n+=Ue(e[0]));for(let r=1;r<t;r++)n+=`, `+Ue(e[r]);return n+=`]`,n}let n=/\[object ([^\]]+)\]/.exec(toString.call(e)),r;if(n&&n.length>1)r=n[1];else return toString.call(e);if(r==`Object`)try{return`Object(`+JSON.stringify(e)+`)`}catch{return`Object`}return e instanceof Error?`${e.name}: ${e.message}
${e.stack}`:r}function F(e,t){e>>>=0;let n=R(),r=[];for(let i=e;i<e+4*t;i+=4)r.push(Y.__wbindgen_externrefs.get(n.getUint32(i,!0)));return Y.__externref_drop_slice(e,t),r}function We(e,t){return e>>>=0,V().subarray(e/1,e/1+t)}let I=null;function Ge(){return(I===null||I.byteLength===0)&&(I=new BigUint64Array(Y.memory.buffer)),I}let L=null;function R(){return(L===null||L.buffer.detached===!0||L.buffer.detached===void 0&&L.buffer!==Y.memory.buffer)&&(L=new DataView(Y.memory.buffer)),L}function z(e,t){return Xe(e>>>0,t)}let B=null;function V(){return(B===null||B.byteLength===0)&&(B=new Uint8Array(Y.memory.buffer)),B}function H(e,t){try{return e.apply(this,t)}catch(e){let t=Ve(e);Y.__wbindgen_exn_store(t)}}function U(e){return e==null}function Ke(e,t){let n=t(e.length*8,8)>>>0;return Ge().set(e,n/8),J=e.length,n}function qe(e,t){let n=t(e.length*1,1)>>>0;return V().set(e,n/1),J=e.length,n}function Je(e,t){let n=t(e.length*4,4)>>>0;for(let t=0;t<e.length;t++){let r=Ve(e[t]);R().setUint32(n+4*t,r,!0)}return J=e.length,n}function W(e,t,n){if(n===void 0){let n=q.encode(e),r=t(n.length,1)>>>0;return V().subarray(r,r+n.length).set(n),J=n.length,r}let r=e.length,i=t(r,1)>>>0,a=V(),o=0;for(;o<r;o++){let t=e.charCodeAt(o);if(t>127)break;a[i+o]=t}if(o!==r){o!==0&&(e=e.slice(o)),i=n(i,r,r=o+e.length*3,1)>>>0;let t=V().subarray(i+o,i+r),a=q.encodeInto(e,t);o+=a.written,i=n(i,r,o,1)>>>0}return J=o,i}function G(e){let t=Y.__wbindgen_externrefs.get(e);return Y.__externref_table_dealloc(e),t}let K=new TextDecoder(`utf-8`,{ignoreBOM:!0,fatal:!0});K.decode();let Ye=0;function Xe(e,t){return Ye+=t,Ye>=2146435072&&(K=new TextDecoder(`utf-8`,{ignoreBOM:!0,fatal:!0}),K.decode(),Ye=t),K.decode(V().subarray(e,e+t))}let q=new TextEncoder;`encodeInto`in q||(q.encodeInto=function(e,t){let n=q.encode(e);return t.set(n),{read:e.length,written:n.length}});let J=0,Y;function Ze(e,t){return Y=e.exports,I=null,L=null,B=null,Y.__wbindgen_start(),Y}async function Qe(e,t){if(typeof Response==`function`&&e instanceof Response){if(!e.ok)throw Error(`failed to fetch Wasm: ${e.status} ${e.statusText} fetching '${e.url}'`);if(typeof WebAssembly.instantiateStreaming==`function`)try{return await WebAssembly.instantiateStreaming(e,t)}catch(t){if(n(e.type)&&e.headers.get(`Content-Type`)!==`application/wasm`)console.warn("`WebAssembly.instantiateStreaming` failed because your server does not serve Wasm with `application/wasm` MIME type. Falling back to `WebAssembly.instantiate` which is slower. Original error:\n",t);else throw t}let r=await e.arrayBuffer();return await WebAssembly.instantiate(r,t)}{let n=await WebAssembly.instantiate(e,t);return n instanceof WebAssembly.Instance?{instance:n,module:e}:n}function n(e){switch(e){case`basic`:case`cors`:case`default`:return!0}return!1}}function $e(e){if(Y!==void 0)return Y;e!==void 0&&(Object.getPrototypeOf(e)===Object.prototype?{module:e}=e:console.warn("using deprecated parameters for `initSync()`; pass a single object instead"));let t=Fe();return e instanceof WebAssembly.Module||(e=new WebAssembly.Module(e)),Ze(new WebAssembly.Instance(e,t))}async function et(e){if(Y!==void 0)return Y;e!==void 0&&(Object.getPrototypeOf(e)===Object.prototype?{module_or_path:e}=e:console.warn(`using deprecated parameters for the initialization function; pass a single object instead`)),e===void 0&&(e=new URL);let t=Fe();(typeof e==`string`||typeof Request==`function`&&e instanceof Request||typeof URL==`function`&&e instanceof URL)&&(e=fetch(e));let{instance:n}=await Qe(await e,t);return Ze(n)}let tt=/* @__PURE__ */ Object.freeze(/* @__PURE__ */ Object.defineProperty({__proto__:null,Dialect:De,Language:Oe,Lint:j,Linter:ke,OrganizedGroup:M,Span:N,Suggestion:P,SuggestionKind:Ae,default:et,get_default_lint_config:je,get_default_lint_config_as_json:Me,initSync:$e,setup:Ne,to_title_case:Pe},Symbol.toStringTag,{value:`Module`})),nt=class e extends Promise{constructor(e){super(e=>{e()}),o(this,c),o(this,l),s(this,c,e)}static from(t){return new e(e=>{e(t())})}static resolve(t){return new e(e=>{e(t)})}static reject(t){return new e((e,n)=>{n(t)})}then(e,t){return a(this,l)??s(this,l,new Promise(a(this,c))),a(this,l).then(e,t)}catch(e){return a(this,l)??s(this,l,new Promise(a(this,c))),a(this,l).catch(e)}finally(e){return a(this,l)??s(this,l,new Promise(a(this,c))),a(this,l).finally(e)}};c=/* @__PURE__ */ new WeakMap,l=/* @__PURE__ */ new WeakMap;let rt=nt,it=(e,t,n,r)=>{if(n===`length`||n===`prototype`||n===`arguments`||n===`caller`)return;let i=Object.getOwnPropertyDescriptor(e,n),a=Object.getOwnPropertyDescriptor(t,n);(at(i,a)||!r)&&Object.defineProperty(e,n,a)},at=function(e,t){return e===void 0||e.configurable||e.writable===t.writable&&e.enumerable===t.enumerable&&e.configurable===t.configurable&&(e.writable||e.value===t.value)},ot=(e,t)=>{let n=Object.getPrototypeOf(t);n!==Object.getPrototypeOf(e)&&Object.setPrototypeOf(e,n)},st=(e,t)=>`/* Wrapped ${e}*/
${t}`,ct=Object.getOwnPropertyDescriptor(Function.prototype,`toString`),lt=Object.getOwnPropertyDescriptor(Function.prototype.toString,`name`),ut=(e,t,n)=>{let r=n===``?``:`with ${n.trim()}() `,i=st.bind(null,r,t.toString());Object.defineProperty(i,"name",lt),Object.defineProperty(e,"toString",{...ct,value:i})};function dt(e,t,{ignoreNonConfigurable:n=!1}={}){let{name:r}=e;for(let r of Reflect.ownKeys(t))it(e,t,r,n);return ot(e,t),ut(e,t,r),e}let ft=/* @__PURE__ */ new WeakMap;function pt(e,{cacheKey:t=([e])=>e,cache:n=/* @__PURE__ */ new Map}={}){let r=/* @__PURE__ */ new Map,i=function(...i){let a=t(i);if(r.has(a))return r.get(a);let o=(async()=>{try{if(n&&await n.has(a))return await n.get(a);let t=await e.apply(this,i);try{return t}finally{n&&await n.set(a,t)}}finally{r.delete(a)}})();return r.set(a,o),o};return dt(i,e,{ignoreNonConfigurable:!0}),ft.set(i,n),i}function mt(e){return e.includes(`harper_wasm_slim`)?`slim`:`full`}function ht(e){return e===`slim`?Ee:tt}function gt(e,t){return t===`slim`?e:e.includes(`harper_wasm_bg.wasm`)?e.replace(`harper_wasm_bg.wasm`,`harper_wasm_slim_bg.wasm`):null}function _t(e){return typeof process<`u`&&e.startsWith(`file://`)?import(
/* webpackIgnore: true */
/* @vite-ignore */
`fs`).then(t=>new Promise((n,r)=>{t.readFile(new URL(e).pathname,(e,t)=>{e&&r(e),n(t)})})):e}async function vt(e,t){let n=ht(t),r=gt(e,t);if(r!=null)try{await Te({module_or_path:_t(r)})}catch(e){if(t===`slim`)throw e}return await n.default({module_or_path:_t(e)}),n}let yt={full:pt(e=>vt(e,`full`)),slim:pt(e=>vt(e,`slim`))};function bt(e,t){return yt[t](e)}var xt=class{constructor(){r(this,`url`,``),r(this,`glueFlavor`,`full`),r(this,`inner`,null)}static create(e,t){let n=new St;return n.url=e,n.glueFlavor=t??mt(typeof e==`string`?e:e.href),n.inner=rt.from(()=>bt(typeof n.url==`string`?n.url:n.url.href,n.glueFlavor)),n}async getDefaultLintConfigAsJSON(){return(await this.inner).get_default_lint_config_as_json()}async getDefaultLintConfig(){return(await this.inner).get_default_lint_config()}async toTitleCase(e){return(await this.inner).to_title_case(e)}async setup(){(await this.inner).setup()}},St=class extends xt{async createLinter(e){return(await this.getBinaryModule()).Linter.new(e??u.American)}async getBinaryModule(){return await rt.from(()=>bt(typeof this.url==`string`?this.url:this.url.href,this.glueFlavor))}},Ct=Object.defineProperty,wt=(e,t,n)=>t in e?Ct(e,t,{enumerable:!0,configurable:!0,writable:!0,value:n}):e[t]=n,Tt=(e,t,n)=>wt(e,typeof t==`symbol`?t:t+``,n);function Et(e){switch(e){case`plaintext`:return d.Plain;case`typst`:return d.Typst;case`markdown`:case void 0:return d.Markdown;default:return console.warn(`Unknown Harper language '${String(e)}'; using markdown.`),d.Markdown}}function Dt(e,t){return[e,Et(t?.language),t?.forceAllHeadings??!1,t?.regex_mask,t?.dedup??!0,t?.isolateEnglish??!1]}var Ot=class{constructor(e){Tt(this,`binary`),Tt(this,`inner`),Tt(this,`disposed`,!1),this.binary=e.binary,this.binary.setup(),this.inner=this.createInner(e.dialect)}createInner(e){return rt.from(async()=>(await this.binary.setup(),this.binary.createLinter(e)))}async setup(){await this.lint(``,{language:`plaintext`});let e=await this.exportIgnoredLints();await this.importIgnoredLints(e)}async lint(e,t){return(await this.inner).lint(...Dt(e,t))}async organizedLints(e,t){let n=(await this.inner).organized_lints(...Dt(e,t)),r={};for(let e of n)r[e.group]=e.lints,e.free();return r}async applySuggestion(e,t,n){return(await this.inner).apply_suggestion(e,t,n)}async isLikelyEnglish(e){return(await this.inner).is_likely_english(e)}async isolateEnglish(e){return(await this.inner).isolate_english(e)}async getLintConfig(){return(await this.inner).get_lint_config_as_object()}async getDefaultLintConfigAsJSON(){return await this.binary.getDefaultLintConfigAsJSON()}async getDefaultLintConfig(){return await this.binary.getDefaultLintConfig()}async getStructuredLintConfig(){return(await this.inner).get_structured_lint_config_as_object()}async getStructuredLintConfigJSON(){return(await this.inner).get_structured_lint_config_as_json()}async setLintConfig(e){(await this.inner).set_lint_config_from_object(e)}async getLintConfigAsJSON(){return(await this.inner).get_lint_config_as_json()}async setLintConfigWithJSON(e){(await this.inner).set_lint_config_from_json(e)}async toTitleCase(e){return await this.binary.toTitleCase(e)}async getLintDescriptions(){return(await this.inner).get_lint_descriptions_as_object()}async getLintDescriptionsAsJSON(){return(await this.inner).get_lint_descriptions_as_json()}async getLintDescriptionsHTML(){return(await this.inner).get_lint_descriptions_html_as_object()}async getLintDescriptionsHTMLAsJSON(){return(await this.inner).get_lint_descriptions_html_as_json()}async ignoreLint(e,t){return await this.ignoreLints(e,[t])}async ignoreLints(e,t){(await this.inner).ignore_lints(e,t)}async ignoreLintHash(e){(await this.inner).ignore_hashes(new BigUint64Array([e]))}async exportIgnoredLints(){return(await this.inner).export_ignored_lints()}async importIgnoredLints(e){(await this.inner).import_ignored_lints(e)}async contextHash(e,t){return(await this.inner).context_hash(e,t)}async clearIgnoredLints(){(await this.inner).clear_ignored_lints()}async clearWords(){return(await this.inner).clear_words()}async importWords(e){return(await this.inner).import_words(e)}async exportWords(){return(await this.inner).export_words()}async getDialect(){return(await this.inner).get_dialect()}async setDialect(e){let t=await this.inner;return t.get_dialect()!==e&&(t.free(),this.inner=this.createInner(e)),Promise.resolve()}async summarizeStats(e,t){return(await this.inner).summarize_stats(e,t)}async generateStatsFile(){return(await this.inner).generate_stats_file()}async importStatsFile(e){return(await this.inner).import_stats_file(e)}async loadWeirpackFromBlob(e){let t=new Uint8Array(await e.arrayBuffer());return this.loadWeirpackFromBytes(t)}async loadWeirpackFromBytes(e){let t=await this.inner,n=e instanceof Uint8Array?e:Uint8Array.from(e);return t.import_weirpack(n)}async dispose(){this.disposed||(this.disposed=!0,(await this.inner).free())}};typeof self<`u`&&self.Blob&&new Blob([`URL.revokeObjectURL(import.meta.url);`,`var __defProp = Object.defineProperty;
var __typeError = (msg) => {
  throw TypeError(msg);
};
var __defNormalProp = (obj, key, value) => key in obj ? __defProp(obj, key, { enumerable: true, configurable: true, writable: true, value }) : obj[key] = value;
var __publicField = (obj, key, value) => __defNormalProp(obj, typeof key !== "symbol" ? key + "" : key, value);
var __accessCheck = (obj, member, msg) => member.has(obj) || __typeError("Cannot " + msg);
var __privateGet = (obj, member, getter) => (__accessCheck(obj, member, "read from private field"), getter ? getter.call(obj) : member.get(obj));
var __privateAdd = (obj, member, value) => member.has(obj) ? __typeError("Cannot add the same private member more than once") : member instanceof WeakSet ? member.add(obj) : member.set(obj, value);
var __privateSet = (obj, member, value, setter) => (__accessCheck(obj, member, "write to private field"), setter ? setter.call(obj, value) : member.set(obj, value), value);
var _executor, _promise;
const Dialect$1 = Object.freeze({
  American: 0,
  "0": "American",
  British: 1,
  "1": "British",
  Australian: 2,
  "2": "Australian",
  Canadian: 3,
  "3": "Canadian",
  Indian: 4,
  "4": "Indian"
});
const Language$1 = Object.freeze({
  Plain: 0,
  "0": "Plain",
  Markdown: 1,
  "1": "Markdown",
  Typst: 2,
  "2": "Typst"
});
let Lint$1 = class Lint {
  static __wrap(ptr) {
    const obj = Object.create(Lint.prototype);
    obj.__wbg_ptr = ptr;
    LintFinalization$1.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  static __unwrap(jsValue) {
    if (!(jsValue instanceof Lint)) {
      return 0;
    }
    return jsValue.__destroy_into_raw();
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    LintFinalization$1.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm$1.__wbg_lint_free(ptr, 0);
  }
  /**
   * @param {string} json
   * @returns {Lint}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0$1(json, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.lint_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0$1(ret[1]);
    }
    return Lint.__wrap(ret[0]);
  }
  /**
   * Get the content of the source material pointed to by [\`Self::span\`]
   * @returns {string}
   */
  get_problem_text() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_get_problem_text(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a string representing the general category of the lint.
   * @returns {string}
   */
  lint_kind() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_lint_kind(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a string representing the general category of the lint.
   * @returns {string}
   */
  lint_kind_pretty() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_lint_kind_pretty(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a description of the error.
   * @returns {string}
   */
  message() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_message(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a description of the error as HTML.
   * @returns {string}
   */
  message_html() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_message_html(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get the location of the problematic text.
   * @returns {Span}
   */
  span() {
    const ret = wasm$1.lint_span(this.__wbg_ptr);
    return Span$1.__wrap(ret);
  }
  /**
   * Equivalent to calling \`.length\` on the result of \`suggestions()\`.
   * @returns {number}
   */
  suggestion_count() {
    const ret = wasm$1.lint_suggestion_count(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * Get an array of any suggestions that may resolve the issue.
   * @returns {Suggestion[]}
   */
  suggestions() {
    const ret = wasm$1.lint_suggestions(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0$1(ret[0], ret[1]);
    wasm$1.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.lint_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
};
if (Symbol.dispose) Lint$1.prototype[Symbol.dispose] = Lint$1.prototype.free;
let Linter$1 = class Linter {
  static __wrap(ptr) {
    const obj = Object.create(Linter.prototype);
    obj.__wbg_ptr = ptr;
    LinterFinalization$1.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    LinterFinalization$1.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm$1.__wbg_linter_free(ptr, 0);
  }
  /**
   * Apply a suggestion from a given lint.
   * This action will be logged to the Linter's statistics.
   * @param {string} source_text
   * @param {Lint} lint
   * @param {Suggestion} suggestion
   * @returns {string}
   */
  apply_suggestion(source_text, lint, suggestion) {
    let deferred3_0;
    let deferred3_1;
    try {
      const ptr0 = passStringToWasm0$1(source_text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      const len0 = WASM_VECTOR_LEN$1;
      _assertClass$1(lint, Lint$1);
      _assertClass$1(suggestion, Suggestion$1);
      const ret = wasm$1.linter_apply_suggestion(this.__wbg_ptr, ptr0, len0, lint.__wbg_ptr, suggestion.__wbg_ptr);
      var ptr2 = ret[0];
      var len2 = ret[1];
      if (ret[3]) {
        ptr2 = 0;
        len2 = 0;
        throw takeFromExternrefTable0$1(ret[2]);
      }
      deferred3_0 = ptr2;
      deferred3_1 = len2;
      return getStringFromWasm0$1(ptr2, len2);
    } finally {
      wasm$1.__wbindgen_free(deferred3_0, deferred3_1, 1);
    }
  }
  clear_ignored_lints() {
    wasm$1.linter_clear_ignored_lints(this.__wbg_ptr);
  }
  /**
   * Clear the user dictionary.
   */
  clear_words() {
    wasm$1.linter_clear_words(this.__wbg_ptr);
  }
  /**
   * Compute the context hash of a given lint.
   * @param {string} source_text
   * @param {Lint} lint
   * @returns {bigint}
   */
  context_hash(source_text, lint) {
    const ptr0 = passStringToWasm0$1(source_text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    _assertClass$1(lint, Lint$1);
    const ret = wasm$1.linter_context_hash(this.__wbg_ptr, ptr0, len0, lint.__wbg_ptr);
    return BigInt.asUintN(64, ret);
  }
  /**
   * Export the linter's ignored lints as a privacy-respecting JSON list of hashes.
   * @returns {string}
   */
  export_ignored_lints() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_export_ignored_lints(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Export words from the dictionary.
   * Note: this will only return words previously added by [\`Self::import_words\`].
   * @returns {string[]}
   */
  export_words() {
    const ret = wasm$1.linter_export_words(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0$1(ret[0], ret[1]);
    wasm$1.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @returns {string}
   */
  generate_stats_file() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_generate_stats_file(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get the dialect this struct was constructed for.
   * @returns {Dialect}
   */
  get_dialect() {
    const ret = wasm$1.linter_get_dialect(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  get_lint_config_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_get_lint_config_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {any}
   */
  get_lint_config_as_object() {
    const ret = wasm$1.linter_get_lint_config_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Get a JSON map containing the descriptions of all the linting rules, formatted as Markdown.
   * @returns {string}
   */
  get_lint_descriptions_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_get_lint_descriptions_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a Record containing the descriptions of all the linting rules, formatted as Markdown.
   * @returns {any}
   */
  get_lint_descriptions_as_object() {
    const ret = wasm$1.linter_get_lint_descriptions_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Get a JSON map containing the descriptions of all the linting rules, formatted as HTML.
   * @returns {string}
   */
  get_lint_descriptions_html_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_get_lint_descriptions_html_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a Record containing the descriptions of all the linting rules, formatted as HTML.
   * @returns {any}
   */
  get_lint_descriptions_html_as_object() {
    const ret = wasm$1.linter_get_lint_descriptions_html_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  get_structured_lint_config_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.linter_get_structured_lint_config_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {any}
   */
  get_structured_lint_config_as_object() {
    const ret = wasm$1.linter_get_structured_lint_config_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Add a specific context hash to the ignored lints list.
   * @param {BigUint64Array} hashes
   */
  ignore_hashes(hashes) {
    const ptr0 = passArray64ToWasm0$1(hashes, wasm$1.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN$1;
    wasm$1.linter_ignore_hashes(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * @param {string} source_text
   * @param {Lint[]} lints
   */
  ignore_lints(source_text, lints) {
    const ptr0 = passStringToWasm0$1(source_text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ptr1 = passArrayJsValueToWasm0$1(lints, wasm$1.__wbindgen_malloc);
    const len1 = WASM_VECTOR_LEN$1;
    wasm$1.linter_ignore_lints(this.__wbg_ptr, ptr0, len0, ptr1, len1);
  }
  /**
   * Import into the linter's ignored lints from a privacy-respecting JSON list of hashes.
   * @param {string} json
   */
  import_ignored_lints(json) {
    const ptr0 = passStringToWasm0$1(json, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_import_ignored_lints(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0$1(ret[0]);
    }
  }
  /**
   * @param {string} file
   */
  import_stats_file(file) {
    const ptr0 = passStringToWasm0$1(file, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_import_stats_file(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0$1(ret[0]);
    }
  }
  /**
   * Load a Weirpack from raw bytes, merging its rules into the current linter.
   * Returns test failures if any are found, and does not import in that case.
   * @param {Uint8Array} bytes
   * @returns {any}
   */
  import_weirpack(bytes) {
    const ptr0 = passArray8ToWasm0$1(bytes, wasm$1.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_import_weirpack(this.__wbg_ptr, ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0$1(ret[1]);
    }
    return takeFromExternrefTable0$1(ret[0]);
  }
  /**
   * Import words into the dictionary.
   * @param {string[]} additional_words
   */
  import_words(additional_words) {
    const ptr0 = passArrayJsValueToWasm0$1(additional_words, wasm$1.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN$1;
    wasm$1.linter_import_words(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * Helper method to quickly check if a plain string is likely intended to be English
   * @param {string} text
   * @returns {boolean}
   */
  is_likely_english(text) {
    const ptr0 = passStringToWasm0$1(text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_is_likely_english(this.__wbg_ptr, ptr0, len0);
    return ret !== 0;
  }
  /**
   * Helper method to remove non-English text from a plain English document.
   * @param {string} text
   * @returns {string}
   */
  isolate_english(text) {
    let deferred2_0;
    let deferred2_1;
    try {
      const ptr0 = passStringToWasm0$1(text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      const len0 = WASM_VECTOR_LEN$1;
      const ret = wasm$1.linter_isolate_english(this.__wbg_ptr, ptr0, len0);
      deferred2_0 = ret[0];
      deferred2_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred2_0, deferred2_1, 1);
    }
  }
  /**
   * Perform the configured linting on the provided text.
   *
   * If the provided regex mask cannot be parsed, this method will return an empty array.
   * @param {string} text
   * @param {Language} language
   * @param {boolean} all_headings
   * @param {string | null | undefined} regex_mask
   * @param {boolean} dedup
   * @param {boolean} isolate_english
   * @returns {Lint[]}
   */
  lint(text, language, all_headings, regex_mask, dedup, isolate_english) {
    const ptr0 = passStringToWasm0$1(text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    var ptr1 = isLikeNone$1(regex_mask) ? 0 : passStringToWasm0$1(regex_mask, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    var len1 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_lint(this.__wbg_ptr, ptr0, len0, language, all_headings, ptr1, len1, dedup, isolate_english);
    var v3 = getArrayJsValueFromWasm0$1(ret[0], ret[1]);
    wasm$1.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v3;
  }
  /**
   * Construct a new \`Linter\`.
   * Note that this can mean constructing the curated dictionary, which is the most expensive operation
   * in Harper.
   * @param {Dialect} dialect
   * @returns {Linter}
   */
  static new(dialect) {
    const ret = wasm$1.linter_new(dialect);
    return Linter.__wrap(ret);
  }
  /**
   * @param {string} text
   * @param {Language} language
   * @param {boolean} all_headings
   * @param {string | null | undefined} regex_mask
   * @param {boolean} dedup
   * @param {boolean} isolate_english
   * @returns {OrganizedGroup[]}
   */
  organized_lints(text, language, all_headings, regex_mask, dedup, isolate_english) {
    const ptr0 = passStringToWasm0$1(text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    var ptr1 = isLikeNone$1(regex_mask) ? 0 : passStringToWasm0$1(regex_mask, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    var len1 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_organized_lints(this.__wbg_ptr, ptr0, len0, language, all_headings, ptr1, len1, dedup, isolate_english);
    var v3 = getArrayJsValueFromWasm0$1(ret[0], ret[1]);
    wasm$1.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v3;
  }
  /**
   * @param {string} json
   */
  set_lint_config_from_json(json) {
    const ptr0 = passStringToWasm0$1(json, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.linter_set_lint_config_from_json(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0$1(ret[0]);
    }
  }
  /**
   * @param {any} object
   */
  set_lint_config_from_object(object) {
    const ret = wasm$1.linter_set_lint_config_from_object(this.__wbg_ptr, object);
    if (ret[1]) {
      throw takeFromExternrefTable0$1(ret[0]);
    }
  }
  /**
   * @param {bigint | null} [start_time]
   * @param {bigint | null} [end_time]
   * @returns {any}
   */
  summarize_stats(start_time, end_time) {
    const ret = wasm$1.linter_summarize_stats(this.__wbg_ptr, !isLikeNone$1(start_time), isLikeNone$1(start_time) ? BigInt(0) : start_time, !isLikeNone$1(end_time), isLikeNone$1(end_time) ? BigInt(0) : end_time);
    return ret;
  }
};
if (Symbol.dispose) Linter$1.prototype[Symbol.dispose] = Linter$1.prototype.free;
let OrganizedGroup$1 = class OrganizedGroup {
  static __wrap(ptr) {
    const obj = Object.create(OrganizedGroup.prototype);
    obj.__wbg_ptr = ptr;
    OrganizedGroupFinalization$1.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    OrganizedGroupFinalization$1.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm$1.__wbg_organizedgroup_free(ptr, 0);
  }
  /**
   * @returns {string}
   */
  get group() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.__wbg_get_organizedgroup_group(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {Lint[]}
   */
  get lints() {
    const ret = wasm$1.__wbg_get_organizedgroup_lints(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0$1(ret[0], ret[1]);
    wasm$1.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @param {string} arg0
   */
  set group(arg0) {
    const ptr0 = passStringToWasm0$1(arg0, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    wasm$1.__wbg_set_organizedgroup_group(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * @param {Lint[]} arg0
   */
  set lints(arg0) {
    const ptr0 = passArrayJsValueToWasm0$1(arg0, wasm$1.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN$1;
    wasm$1.__wbg_set_organizedgroup_lints(this.__wbg_ptr, ptr0, len0);
  }
};
if (Symbol.dispose) OrganizedGroup$1.prototype[Symbol.dispose] = OrganizedGroup$1.prototype.free;
let Span$1 = class Span {
  static __wrap(ptr) {
    const obj = Object.create(Span.prototype);
    obj.__wbg_ptr = ptr;
    SpanFinalization$1.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    SpanFinalization$1.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm$1.__wbg_span_free(ptr, 0);
  }
  /**
   * @returns {number}
   */
  get end() {
    const ret = wasm$1.__wbg_get_span_end(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @returns {number}
   */
  get start() {
    const ret = wasm$1.__wbg_get_span_start(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @param {number} arg0
   */
  set end(arg0) {
    wasm$1.__wbg_set_span_end(this.__wbg_ptr, arg0);
  }
  /**
   * @param {number} arg0
   */
  set start(arg0) {
    wasm$1.__wbg_set_span_start(this.__wbg_ptr, arg0);
  }
  /**
   * @param {string} json
   * @returns {Span}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0$1(json, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.span_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0$1(ret[1]);
    }
    return Span.__wrap(ret[0]);
  }
  /**
   * @returns {boolean}
   */
  is_empty() {
    const ret = wasm$1.span_is_empty(this.__wbg_ptr);
    return ret !== 0;
  }
  /**
   * @returns {number}
   */
  len() {
    const ret = wasm$1.span_len(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @param {number} start
   * @param {number} end
   * @returns {Span}
   */
  static new(start, end) {
    const ret = wasm$1.span_new(start, end);
    return Span.__wrap(ret);
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.span_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
};
if (Symbol.dispose) Span$1.prototype[Symbol.dispose] = Span$1.prototype.free;
let Suggestion$1 = class Suggestion {
  static __wrap(ptr) {
    const obj = Object.create(Suggestion.prototype);
    obj.__wbg_ptr = ptr;
    SuggestionFinalization$1.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    SuggestionFinalization$1.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm$1.__wbg_suggestion_free(ptr, 0);
  }
  /**
   * @param {string} json
   * @returns {Suggestion}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0$1(json, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.suggestion_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0$1(ret[1]);
    }
    return Suggestion.__wrap(ret[0]);
  }
  /**
   * Get the text that is going to replace the problematic section.
   * If [\`Self::kind\`] is \`SuggestionKind::Remove\`, this will return an empty
   * string.
   * @returns {string}
   */
  get_replacement_text() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.suggestion_get_replacement_text(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {SuggestionKind}
   */
  kind() {
    const ret = wasm$1.suggestion_kind(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm$1.suggestion_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0$1(ret[0], ret[1]);
    } finally {
      wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
};
if (Symbol.dispose) Suggestion$1.prototype[Symbol.dispose] = Suggestion$1.prototype.free;
const SuggestionKind$1 = Object.freeze({
  /**
   * Replace the problematic text.
   */
  Replace: 0,
  "0": "Replace",
  /**
   * Remove the problematic text.
   */
  Remove: 1,
  "1": "Remove",
  /**
   * Insert additional text after the error.
   */
  InsertAfter: 2,
  "2": "InsertAfter"
});
function get_default_lint_config$1() {
  const ret = wasm$1.get_default_lint_config();
  return ret;
}
function get_default_lint_config_as_json$1() {
  let deferred1_0;
  let deferred1_1;
  try {
    const ret = wasm$1.get_default_lint_config_as_json();
    deferred1_0 = ret[0];
    deferred1_1 = ret[1];
    return getStringFromWasm0$1(ret[0], ret[1]);
  } finally {
    wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
  }
}
function setup$1() {
  wasm$1.setup();
}
function to_title_case$1(text) {
  let deferred2_0;
  let deferred2_1;
  try {
    const ptr0 = passStringToWasm0$1(text, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN$1;
    const ret = wasm$1.to_title_case(ptr0, len0);
    deferred2_0 = ret[0];
    deferred2_1 = ret[1];
    return getStringFromWasm0$1(ret[0], ret[1]);
  } finally {
    wasm$1.__wbindgen_free(deferred2_0, deferred2_1, 1);
  }
}
function __wbg_get_imports$1() {
  const import0 = {
    __proto__: null,
    __wbg_Error_408e67f47ca7b58b: function(arg0, arg1) {
      const ret = Error(getStringFromWasm0$1(arg0, arg1));
      return ret;
    },
    __wbg_String_8564e559799eccda: function(arg0, arg1) {
      const ret = String(arg1);
      const ptr1 = passStringToWasm0$1(ret, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN$1;
      getDataViewMemory0$1().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0$1().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_boolean_get_c9c83ebd41b34df3: function(arg0) {
      const v = arg0;
      const ret = typeof v === "boolean" ? v : void 0;
      return isLikeNone$1(ret) ? 16777215 : ret ? 1 : 0;
    },
    __wbg___wbindgen_debug_string_a57024b9c6e4a48b: function(arg0, arg1) {
      const ret = debugString$1(arg1);
      const ptr1 = passStringToWasm0$1(ret, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN$1;
      getDataViewMemory0$1().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0$1().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_is_function_5e4570eb24ffa122: function(arg0) {
      const ret = typeof arg0 === "function";
      return ret;
    },
    __wbg___wbindgen_is_object_a2790eb24c211ea0: function(arg0) {
      const val = arg0;
      const ret = typeof val === "object" && val !== null;
      return ret;
    },
    __wbg___wbindgen_is_string_e6f02f0ea5f20a32: function(arg0) {
      const ret = typeof arg0 === "string";
      return ret;
    },
    __wbg___wbindgen_jsval_loose_eq_acf2776254a8d832: function(arg0, arg1) {
      const ret = arg0 == arg1;
      return ret;
    },
    __wbg___wbindgen_number_get_136b9679cab35cfb: function(arg0, arg1) {
      const obj = arg1;
      const ret = typeof obj === "number" ? obj : void 0;
      getDataViewMemory0$1().setFloat64(arg0 + 8 * 1, isLikeNone$1(ret) ? 0 : ret, true);
      getDataViewMemory0$1().setInt32(arg0 + 4 * 0, !isLikeNone$1(ret), true);
    },
    __wbg___wbindgen_string_get_d154f1e671052120: function(arg0, arg1) {
      const obj = arg1;
      const ret = typeof obj === "string" ? obj : void 0;
      var ptr1 = isLikeNone$1(ret) ? 0 : passStringToWasm0$1(ret, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      var len1 = WASM_VECTOR_LEN$1;
      getDataViewMemory0$1().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0$1().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_throw_bb96b2010945f0bc: function(arg0, arg1) {
      throw new Error(getStringFromWasm0$1(arg0, arg1));
    },
    __wbg_call_1c5886ab9c57d1c7: function() {
      return handleError$1(function(arg0, arg1) {
        const ret = arg0.call(arg1);
        return ret;
      }, arguments);
    },
    __wbg_done_669171204c3dcae2: function(arg0) {
      const ret = arg0.done;
      return ret;
    },
    __wbg_entries_7774d489e1da5f4f: function(arg0) {
      const ret = Object.entries(arg0);
      return ret;
    },
    __wbg_error_757e9472f8410341: function(arg0, arg1) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.error(getStringFromWasm0$1(arg0, arg1));
      } finally {
        wasm$1.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_getRandomValues_a608c4436c19407a: function() {
      return handleError$1(function(arg0, arg1) {
        globalThis.crypto.getRandomValues(getArrayU8FromWasm0$1(arg0, arg1));
      }, arguments);
    },
    __wbg_getRandomValues_e446ea5ffdd14ee5: function() {
      return handleError$1(function(arg0, arg1) {
        globalThis.crypto.getRandomValues(getArrayU8FromWasm0$1(arg0, arg1));
      }, arguments);
    },
    __wbg_getTime_63fb0332e6c4ec17: function(arg0) {
      const ret = arg0.getTime();
      return ret;
    },
    __wbg_get_c0c8f8d7da0c03dd: function(arg0, arg1) {
      const ret = arg0[arg1 >>> 0];
      return ret;
    },
    __wbg_get_d173c0308df22d37: function() {
      return handleError$1(function(arg0, arg1) {
        const ret = Reflect.get(arg0, arg1);
        return ret;
      }, arguments);
    },
    __wbg_get_unchecked_e20b893aeafc3fca: function(arg0, arg1) {
      const ret = arg0[arg1 >>> 0];
      return ret;
    },
    __wbg_instanceof_ArrayBuffer_993d02d2d254cad1: function(arg0) {
      let result;
      try {
        result = arg0 instanceof ArrayBuffer;
      } catch (_) {
        result = false;
      }
      const ret = result;
      return ret;
    },
    __wbg_instanceof_Uint8Array_f935dbb0aa7cdeed: function(arg0) {
      let result;
      try {
        result = arg0 instanceof Uint8Array;
      } catch (_) {
        result = false;
      }
      const ret = result;
      return ret;
    },
    __wbg_iterator_5cebbb86e33c6dd6: function() {
      const ret = Symbol.iterator;
      return ret;
    },
    __wbg_length_36bd29c6848c2144: function(arg0) {
      const ret = arg0.length;
      return ret;
    },
    __wbg_length_ecfa2c63d3d0d82c: function(arg0) {
      const ret = arg0.length;
      return ret;
    },
    __wbg_lint_new: function(arg0) {
      const ret = Lint$1.__wrap(arg0);
      return ret;
    },
    __wbg_lint_unwrap: function(arg0) {
      const ret = Lint$1.__unwrap(arg0);
      return ret;
    },
    __wbg_log_1f8cbb01c83d06c2: function(arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.log(getStringFromWasm0$1(arg0, arg1), getStringFromWasm0$1(arg2, arg3), getStringFromWasm0$1(arg4, arg5), getStringFromWasm0$1(arg6, arg7));
      } finally {
        wasm$1.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_log_a54ca6b45e09078a: function(arg0, arg1) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.log(getStringFromWasm0$1(arg0, arg1));
      } finally {
        wasm$1.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_mark_6b7f03786f5e4d61: function(arg0, arg1) {
      performance.mark(getStringFromWasm0$1(arg0, arg1));
    },
    __wbg_measure_0e21b33a1c6e3a29: function() {
      return handleError$1(function(arg0, arg1, arg2, arg3) {
        let deferred0_0;
        let deferred0_1;
        let deferred1_0;
        let deferred1_1;
        try {
          deferred0_0 = arg0;
          deferred0_1 = arg1;
          deferred1_0 = arg2;
          deferred1_1 = arg3;
          performance.measure(getStringFromWasm0$1(arg0, arg1), getStringFromWasm0$1(arg2, arg3));
        } finally {
          wasm$1.__wbindgen_free(deferred0_0, deferred0_1, 1);
          wasm$1.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
      }, arguments);
    },
    __wbg_new_0_f117d868b403dc07: function() {
      const ret = /* @__PURE__ */ new Date();
      return ret;
    },
    __wbg_new_116be93542d39019: function() {
      const ret = new Array();
      return ret;
    },
    __wbg_new_227d7c05414eb861: function() {
      const ret = new Error();
      return ret;
    },
    __wbg_new_77cc4f4f472aeb81: function(arg0) {
      const ret = new Uint8Array(arg0);
      return ret;
    },
    __wbg_new_cdf041679ded4c5f: function() {
      const ret = /* @__PURE__ */ new Map();
      return ret;
    },
    __wbg_new_ebe3e0f6837f0879: function() {
      const ret = new Object();
      return ret;
    },
    __wbg_next_42cf16ee0dafc9e2: function() {
      return handleError$1(function(arg0) {
        const ret = arg0.next();
        return ret;
      }, arguments);
    },
    __wbg_next_8f26b64fa5e9f64b: function(arg0) {
      const ret = arg0.next;
      return ret;
    },
    __wbg_organizedgroup_new: function(arg0) {
      const ret = OrganizedGroup$1.__wrap(arg0);
      return ret;
    },
    __wbg_prototypesetcall_de8e0d9553586985: function(arg0, arg1, arg2) {
      Uint8Array.prototype.set.call(getArrayU8FromWasm0$1(arg0, arg1), arg2);
    },
    __wbg_set_014226dfeca53178: function(arg0, arg1, arg2) {
      const ret = arg0.set(arg1, arg2);
      return ret;
    },
    __wbg_set_6be42768c690e380: function(arg0, arg1, arg2) {
      arg0[arg1] = arg2;
    },
    __wbg_set_a80955eb93b145c6: function(arg0, arg1, arg2) {
      arg0[arg1 >>> 0] = arg2;
    },
    __wbg_stack_3b0d974bbf31e44f: function(arg0, arg1) {
      const ret = arg1.stack;
      const ptr1 = passStringToWasm0$1(ret, wasm$1.__wbindgen_malloc, wasm$1.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN$1;
      getDataViewMemory0$1().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0$1().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg_suggestion_new: function(arg0) {
      const ret = Suggestion$1.__wrap(arg0);
      return ret;
    },
    __wbg_value_1e2369fab29b420e: function(arg0) {
      const ret = arg0.value;
      return ret;
    },
    __wbindgen_cast_0000000000000001: function(arg0) {
      const ret = arg0;
      return ret;
    },
    __wbindgen_cast_0000000000000002: function(arg0, arg1) {
      const ret = getStringFromWasm0$1(arg0, arg1);
      return ret;
    },
    __wbindgen_init_externref_table: function() {
      const table = wasm$1.__wbindgen_externrefs;
      const offset = table.grow(4);
      table.set(0, void 0);
      table.set(offset + 0, void 0);
      table.set(offset + 1, null);
      table.set(offset + 2, true);
      table.set(offset + 3, false);
    }
  };
  return {
    __proto__: null,
    "./harper_wasm_slim_bg.js": import0
  };
}
const LintFinalization$1 = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm$1.__wbg_lint_free(ptr, 1));
const LinterFinalization$1 = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm$1.__wbg_linter_free(ptr, 1));
const OrganizedGroupFinalization$1 = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm$1.__wbg_organizedgroup_free(ptr, 1));
const SpanFinalization$1 = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm$1.__wbg_span_free(ptr, 1));
const SuggestionFinalization$1 = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm$1.__wbg_suggestion_free(ptr, 1));
function addToExternrefTable0$1(obj) {
  const idx = wasm$1.__externref_table_alloc();
  wasm$1.__wbindgen_externrefs.set(idx, obj);
  return idx;
}
function _assertClass$1(instance, klass) {
  if (!(instance instanceof klass)) {
    throw new Error(\`expected instance of \${klass.name}\`);
  }
}
function debugString$1(val) {
  const type = typeof val;
  if (type == "number" || type == "boolean" || val == null) {
    return \`\${val}\`;
  }
  if (type == "string") {
    return \`"\${val}"\`;
  }
  if (type == "symbol") {
    const description = val.description;
    if (description == null) {
      return "Symbol";
    } else {
      return \`Symbol(\${description})\`;
    }
  }
  if (type == "function") {
    const name = val.name;
    if (typeof name == "string" && name.length > 0) {
      return \`Function(\${name})\`;
    } else {
      return "Function";
    }
  }
  if (Array.isArray(val)) {
    const length = val.length;
    let debug = "[";
    if (length > 0) {
      debug += debugString$1(val[0]);
    }
    for (let i = 1; i < length; i++) {
      debug += ", " + debugString$1(val[i]);
    }
    debug += "]";
    return debug;
  }
  const builtInMatches = /\\[object ([^\\]]+)\\]/.exec(toString.call(val));
  let className;
  if (builtInMatches && builtInMatches.length > 1) {
    className = builtInMatches[1];
  } else {
    return toString.call(val);
  }
  if (className == "Object") {
    try {
      return "Object(" + JSON.stringify(val) + ")";
    } catch (_) {
      return "Object";
    }
  }
  if (val instanceof Error) {
    return \`\${val.name}: \${val.message}
\${val.stack}\`;
  }
  return className;
}
function getArrayJsValueFromWasm0$1(ptr, len) {
  ptr = ptr >>> 0;
  const mem = getDataViewMemory0$1();
  const result = [];
  for (let i = ptr; i < ptr + 4 * len; i += 4) {
    result.push(wasm$1.__wbindgen_externrefs.get(mem.getUint32(i, true)));
  }
  wasm$1.__externref_drop_slice(ptr, len);
  return result;
}
function getArrayU8FromWasm0$1(ptr, len) {
  ptr = ptr >>> 0;
  return getUint8ArrayMemory0$1().subarray(ptr / 1, ptr / 1 + len);
}
let cachedBigUint64ArrayMemory0$1 = null;
function getBigUint64ArrayMemory0$1() {
  if (cachedBigUint64ArrayMemory0$1 === null || cachedBigUint64ArrayMemory0$1.byteLength === 0) {
    cachedBigUint64ArrayMemory0$1 = new BigUint64Array(wasm$1.memory.buffer);
  }
  return cachedBigUint64ArrayMemory0$1;
}
let cachedDataViewMemory0$1 = null;
function getDataViewMemory0$1() {
  if (cachedDataViewMemory0$1 === null || cachedDataViewMemory0$1.buffer.detached === true || cachedDataViewMemory0$1.buffer.detached === void 0 && cachedDataViewMemory0$1.buffer !== wasm$1.memory.buffer) {
    cachedDataViewMemory0$1 = new DataView(wasm$1.memory.buffer);
  }
  return cachedDataViewMemory0$1;
}
function getStringFromWasm0$1(ptr, len) {
  return decodeText$1(ptr >>> 0, len);
}
let cachedUint8ArrayMemory0$1 = null;
function getUint8ArrayMemory0$1() {
  if (cachedUint8ArrayMemory0$1 === null || cachedUint8ArrayMemory0$1.byteLength === 0) {
    cachedUint8ArrayMemory0$1 = new Uint8Array(wasm$1.memory.buffer);
  }
  return cachedUint8ArrayMemory0$1;
}
function handleError$1(f, args) {
  try {
    return f.apply(this, args);
  } catch (e) {
    const idx = addToExternrefTable0$1(e);
    wasm$1.__wbindgen_exn_store(idx);
  }
}
function isLikeNone$1(x) {
  return x === void 0 || x === null;
}
function passArray64ToWasm0$1(arg, malloc) {
  const ptr = malloc(arg.length * 8, 8) >>> 0;
  getBigUint64ArrayMemory0$1().set(arg, ptr / 8);
  WASM_VECTOR_LEN$1 = arg.length;
  return ptr;
}
function passArray8ToWasm0$1(arg, malloc) {
  const ptr = malloc(arg.length * 1, 1) >>> 0;
  getUint8ArrayMemory0$1().set(arg, ptr / 1);
  WASM_VECTOR_LEN$1 = arg.length;
  return ptr;
}
function passArrayJsValueToWasm0$1(array, malloc) {
  const ptr = malloc(array.length * 4, 4) >>> 0;
  for (let i = 0; i < array.length; i++) {
    const add = addToExternrefTable0$1(array[i]);
    getDataViewMemory0$1().setUint32(ptr + 4 * i, add, true);
  }
  WASM_VECTOR_LEN$1 = array.length;
  return ptr;
}
function passStringToWasm0$1(arg, malloc, realloc) {
  if (realloc === void 0) {
    const buf = cachedTextEncoder$1.encode(arg);
    const ptr2 = malloc(buf.length, 1) >>> 0;
    getUint8ArrayMemory0$1().subarray(ptr2, ptr2 + buf.length).set(buf);
    WASM_VECTOR_LEN$1 = buf.length;
    return ptr2;
  }
  let len = arg.length;
  let ptr = malloc(len, 1) >>> 0;
  const mem = getUint8ArrayMemory0$1();
  let offset = 0;
  for (; offset < len; offset++) {
    const code = arg.charCodeAt(offset);
    if (code > 127) break;
    mem[ptr + offset] = code;
  }
  if (offset !== len) {
    if (offset !== 0) {
      arg = arg.slice(offset);
    }
    ptr = realloc(ptr, len, len = offset + arg.length * 3, 1) >>> 0;
    const view = getUint8ArrayMemory0$1().subarray(ptr + offset, ptr + len);
    const ret = cachedTextEncoder$1.encodeInto(arg, view);
    offset += ret.written;
    ptr = realloc(ptr, len, offset, 1) >>> 0;
  }
  WASM_VECTOR_LEN$1 = offset;
  return ptr;
}
function takeFromExternrefTable0$1(idx) {
  const value = wasm$1.__wbindgen_externrefs.get(idx);
  wasm$1.__externref_table_dealloc(idx);
  return value;
}
let cachedTextDecoder$1 = new TextDecoder("utf-8", { ignoreBOM: true, fatal: true });
cachedTextDecoder$1.decode();
const MAX_SAFARI_DECODE_BYTES$1 = 2146435072;
let numBytesDecoded$1 = 0;
function decodeText$1(ptr, len) {
  numBytesDecoded$1 += len;
  if (numBytesDecoded$1 >= MAX_SAFARI_DECODE_BYTES$1) {
    cachedTextDecoder$1 = new TextDecoder("utf-8", { ignoreBOM: true, fatal: true });
    cachedTextDecoder$1.decode();
    numBytesDecoded$1 = len;
  }
  return cachedTextDecoder$1.decode(getUint8ArrayMemory0$1().subarray(ptr, ptr + len));
}
const cachedTextEncoder$1 = new TextEncoder();
if (!("encodeInto" in cachedTextEncoder$1)) {
  cachedTextEncoder$1.encodeInto = function(arg, view) {
    const buf = cachedTextEncoder$1.encode(arg);
    view.set(buf);
    return {
      read: arg.length,
      written: buf.length
    };
  };
}
let WASM_VECTOR_LEN$1 = 0;
let wasm$1;
function __wbg_finalize_init$1(instance, module) {
  wasm$1 = instance.exports;
  cachedBigUint64ArrayMemory0$1 = null;
  cachedDataViewMemory0$1 = null;
  cachedUint8ArrayMemory0$1 = null;
  wasm$1.__wbindgen_start();
  return wasm$1;
}
async function __wbg_load$1(module, imports) {
  if (typeof Response === "function" && module instanceof Response) {
    if (!module.ok) {
      throw new Error(\`failed to fetch Wasm: \${module.status} \${module.statusText} fetching '\${module.url}'\`);
    }
    if (typeof WebAssembly.instantiateStreaming === "function") {
      try {
        return await WebAssembly.instantiateStreaming(module, imports);
      } catch (e) {
        const validResponse = expectedResponseType(module.type);
        if (validResponse && module.headers.get("Content-Type") !== "application/wasm") {
          console.warn("\`WebAssembly.instantiateStreaming\` failed because your server does not serve Wasm with \`application/wasm\` MIME type. Falling back to \`WebAssembly.instantiate\` which is slower. Original error:\\n", e);
        } else {
          throw e;
        }
      }
    }
    const bytes = await module.arrayBuffer();
    return await WebAssembly.instantiate(bytes, imports);
  } else {
    const instance = await WebAssembly.instantiate(module, imports);
    if (instance instanceof WebAssembly.Instance) {
      return { instance, module };
    } else {
      return instance;
    }
  }
  function expectedResponseType(type) {
    switch (type) {
      case "basic":
      case "cors":
      case "default":
        return true;
    }
    return false;
  }
}
function initSync$1(module) {
  if (wasm$1 !== void 0) return wasm$1;
  if (module !== void 0) {
    if (Object.getPrototypeOf(module) === Object.prototype) {
      ({ module } = module);
    } else {
      console.warn("using deprecated parameters for \`initSync()\`; pass a single object instead");
    }
  }
  const imports = __wbg_get_imports$1();
  if (!(module instanceof WebAssembly.Module)) {
    module = new WebAssembly.Module(module);
  }
  const instance = new WebAssembly.Instance(module, imports);
  return __wbg_finalize_init$1(instance);
}
async function __wbg_init$1(module_or_path) {
  if (wasm$1 !== void 0) return wasm$1;
  if (module_or_path !== void 0) {
    if (Object.getPrototypeOf(module_or_path) === Object.prototype) {
      ({ module_or_path } = module_or_path);
    } else {
      console.warn("using deprecated parameters for the initialization function; pass a single object instead");
    }
  }
  if (module_or_path === void 0) {
    module_or_path = new URL();
  }
  const imports = __wbg_get_imports$1();
  if (typeof module_or_path === "string" || typeof Request === "function" && module_or_path instanceof Request || typeof URL === "function" && module_or_path instanceof URL) {
    module_or_path = fetch(module_or_path);
  }
  const { instance, module } = await __wbg_load$1(await module_or_path, imports);
  return __wbg_finalize_init$1(instance);
}
var defaultGlue = /* @__PURE__ */ Object.freeze({
  __proto__: null,
  Dialect: Dialect$1,
  Language: Language$1,
  Lint: Lint$1,
  Linter: Linter$1,
  OrganizedGroup: OrganizedGroup$1,
  Span: Span$1,
  Suggestion: Suggestion$1,
  SuggestionKind: SuggestionKind$1,
  default: __wbg_init$1,
  get_default_lint_config: get_default_lint_config$1,
  get_default_lint_config_as_json: get_default_lint_config_as_json$1,
  initSync: initSync$1,
  setup: setup$1,
  to_title_case: to_title_case$1
});
const Dialect = Object.freeze({
  American: 0,
  "0": "American",
  British: 1,
  "1": "British",
  Australian: 2,
  "2": "Australian",
  Canadian: 3,
  "3": "Canadian",
  Indian: 4,
  "4": "Indian"
});
const Language = Object.freeze({
  Plain: 0,
  "0": "Plain",
  Markdown: 1,
  "1": "Markdown",
  Typst: 2,
  "2": "Typst"
});
class Lint2 {
  static __wrap(ptr) {
    const obj = Object.create(Lint2.prototype);
    obj.__wbg_ptr = ptr;
    LintFinalization.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  static __unwrap(jsValue) {
    if (!(jsValue instanceof Lint2)) {
      return 0;
    }
    return jsValue.__destroy_into_raw();
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    LintFinalization.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm.__wbg_lint_free(ptr, 0);
  }
  /**
   * @param {string} json
   * @returns {Lint}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0(json, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.lint_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0(ret[1]);
    }
    return Lint2.__wrap(ret[0]);
  }
  /**
   * Get the content of the source material pointed to by [\`Self::span\`]
   * @returns {string}
   */
  get_problem_text() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_get_problem_text(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a string representing the general category of the lint.
   * @returns {string}
   */
  lint_kind() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_lint_kind(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a string representing the general category of the lint.
   * @returns {string}
   */
  lint_kind_pretty() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_lint_kind_pretty(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a description of the error.
   * @returns {string}
   */
  message() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_message(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a description of the error as HTML.
   * @returns {string}
   */
  message_html() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_message_html(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get the location of the problematic text.
   * @returns {Span}
   */
  span() {
    const ret = wasm.lint_span(this.__wbg_ptr);
    return Span2.__wrap(ret);
  }
  /**
   * Equivalent to calling \`.length\` on the result of \`suggestions()\`.
   * @returns {number}
   */
  suggestion_count() {
    const ret = wasm.lint_suggestion_count(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * Get an array of any suggestions that may resolve the issue.
   * @returns {Suggestion[]}
   */
  suggestions() {
    const ret = wasm.lint_suggestions(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0(ret[0], ret[1]);
    wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.lint_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
}
if (Symbol.dispose) Lint2.prototype[Symbol.dispose] = Lint2.prototype.free;
class Linter2 {
  static __wrap(ptr) {
    const obj = Object.create(Linter2.prototype);
    obj.__wbg_ptr = ptr;
    LinterFinalization.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    LinterFinalization.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm.__wbg_linter_free(ptr, 0);
  }
  /**
   * Apply a suggestion from a given lint.
   * This action will be logged to the Linter's statistics.
   * @param {string} source_text
   * @param {Lint} lint
   * @param {Suggestion} suggestion
   * @returns {string}
   */
  apply_suggestion(source_text, lint, suggestion) {
    let deferred3_0;
    let deferred3_1;
    try {
      const ptr0 = passStringToWasm0(source_text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      const len0 = WASM_VECTOR_LEN;
      _assertClass(lint, Lint2);
      _assertClass(suggestion, Suggestion2);
      const ret = wasm.linter_apply_suggestion(this.__wbg_ptr, ptr0, len0, lint.__wbg_ptr, suggestion.__wbg_ptr);
      var ptr2 = ret[0];
      var len2 = ret[1];
      if (ret[3]) {
        ptr2 = 0;
        len2 = 0;
        throw takeFromExternrefTable0(ret[2]);
      }
      deferred3_0 = ptr2;
      deferred3_1 = len2;
      return getStringFromWasm0(ptr2, len2);
    } finally {
      wasm.__wbindgen_free(deferred3_0, deferred3_1, 1);
    }
  }
  clear_ignored_lints() {
    wasm.linter_clear_ignored_lints(this.__wbg_ptr);
  }
  /**
   * Clear the user dictionary.
   */
  clear_words() {
    wasm.linter_clear_words(this.__wbg_ptr);
  }
  /**
   * Compute the context hash of a given lint.
   * @param {string} source_text
   * @param {Lint} lint
   * @returns {bigint}
   */
  context_hash(source_text, lint) {
    const ptr0 = passStringToWasm0(source_text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    _assertClass(lint, Lint2);
    const ret = wasm.linter_context_hash(this.__wbg_ptr, ptr0, len0, lint.__wbg_ptr);
    return BigInt.asUintN(64, ret);
  }
  /**
   * Export the linter's ignored lints as a privacy-respecting JSON list of hashes.
   * @returns {string}
   */
  export_ignored_lints() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_export_ignored_lints(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Export words from the dictionary.
   * Note: this will only return words previously added by [\`Self::import_words\`].
   * @returns {string[]}
   */
  export_words() {
    const ret = wasm.linter_export_words(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0(ret[0], ret[1]);
    wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @returns {string}
   */
  generate_stats_file() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_generate_stats_file(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get the dialect this struct was constructed for.
   * @returns {Dialect}
   */
  get_dialect() {
    const ret = wasm.linter_get_dialect(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  get_lint_config_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_get_lint_config_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {any}
   */
  get_lint_config_as_object() {
    const ret = wasm.linter_get_lint_config_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Get a JSON map containing the descriptions of all the linting rules, formatted as Markdown.
   * @returns {string}
   */
  get_lint_descriptions_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_get_lint_descriptions_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a Record containing the descriptions of all the linting rules, formatted as Markdown.
   * @returns {any}
   */
  get_lint_descriptions_as_object() {
    const ret = wasm.linter_get_lint_descriptions_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Get a JSON map containing the descriptions of all the linting rules, formatted as HTML.
   * @returns {string}
   */
  get_lint_descriptions_html_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_get_lint_descriptions_html_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * Get a Record containing the descriptions of all the linting rules, formatted as HTML.
   * @returns {any}
   */
  get_lint_descriptions_html_as_object() {
    const ret = wasm.linter_get_lint_descriptions_html_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  get_structured_lint_config_as_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.linter_get_structured_lint_config_as_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {any}
   */
  get_structured_lint_config_as_object() {
    const ret = wasm.linter_get_structured_lint_config_as_object(this.__wbg_ptr);
    return ret;
  }
  /**
   * Add a specific context hash to the ignored lints list.
   * @param {BigUint64Array} hashes
   */
  ignore_hashes(hashes) {
    const ptr0 = passArray64ToWasm0(hashes, wasm.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN;
    wasm.linter_ignore_hashes(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * @param {string} source_text
   * @param {Lint[]} lints
   */
  ignore_lints(source_text, lints) {
    const ptr0 = passStringToWasm0(source_text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ptr1 = passArrayJsValueToWasm0(lints, wasm.__wbindgen_malloc);
    const len1 = WASM_VECTOR_LEN;
    wasm.linter_ignore_lints(this.__wbg_ptr, ptr0, len0, ptr1, len1);
  }
  /**
   * Import into the linter's ignored lints from a privacy-respecting JSON list of hashes.
   * @param {string} json
   */
  import_ignored_lints(json) {
    const ptr0 = passStringToWasm0(json, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.linter_import_ignored_lints(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0(ret[0]);
    }
  }
  /**
   * @param {string} file
   */
  import_stats_file(file) {
    const ptr0 = passStringToWasm0(file, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.linter_import_stats_file(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0(ret[0]);
    }
  }
  /**
   * Load a Weirpack from raw bytes, merging its rules into the current linter.
   * Returns test failures if any are found, and does not import in that case.
   * @param {Uint8Array} bytes
   * @returns {any}
   */
  import_weirpack(bytes) {
    const ptr0 = passArray8ToWasm0(bytes, wasm.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.linter_import_weirpack(this.__wbg_ptr, ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0(ret[1]);
    }
    return takeFromExternrefTable0(ret[0]);
  }
  /**
   * Import words into the dictionary.
   * @param {string[]} additional_words
   */
  import_words(additional_words) {
    const ptr0 = passArrayJsValueToWasm0(additional_words, wasm.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN;
    wasm.linter_import_words(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * Helper method to quickly check if a plain string is likely intended to be English
   * @param {string} text
   * @returns {boolean}
   */
  is_likely_english(text) {
    const ptr0 = passStringToWasm0(text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.linter_is_likely_english(this.__wbg_ptr, ptr0, len0);
    return ret !== 0;
  }
  /**
   * Helper method to remove non-English text from a plain English document.
   * @param {string} text
   * @returns {string}
   */
  isolate_english(text) {
    let deferred2_0;
    let deferred2_1;
    try {
      const ptr0 = passStringToWasm0(text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      const len0 = WASM_VECTOR_LEN;
      const ret = wasm.linter_isolate_english(this.__wbg_ptr, ptr0, len0);
      deferred2_0 = ret[0];
      deferred2_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred2_0, deferred2_1, 1);
    }
  }
  /**
   * Perform the configured linting on the provided text.
   *
   * If the provided regex mask cannot be parsed, this method will return an empty array.
   * @param {string} text
   * @param {Language} language
   * @param {boolean} all_headings
   * @param {string | null | undefined} regex_mask
   * @param {boolean} dedup
   * @param {boolean} isolate_english
   * @returns {Lint[]}
   */
  lint(text, language, all_headings, regex_mask, dedup, isolate_english) {
    const ptr0 = passStringToWasm0(text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    var ptr1 = isLikeNone(regex_mask) ? 0 : passStringToWasm0(regex_mask, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    var len1 = WASM_VECTOR_LEN;
    const ret = wasm.linter_lint(this.__wbg_ptr, ptr0, len0, language, all_headings, ptr1, len1, dedup, isolate_english);
    var v3 = getArrayJsValueFromWasm0(ret[0], ret[1]);
    wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v3;
  }
  /**
   * Construct a new \`Linter\`.
   * Note that this can mean constructing the curated dictionary, which is the most expensive operation
   * in Harper.
   * @param {Dialect} dialect
   * @returns {Linter}
   */
  static new(dialect) {
    const ret = wasm.linter_new(dialect);
    return Linter2.__wrap(ret);
  }
  /**
   * @param {string} text
   * @param {Language} language
   * @param {boolean} all_headings
   * @param {string | null | undefined} regex_mask
   * @param {boolean} dedup
   * @param {boolean} isolate_english
   * @returns {OrganizedGroup[]}
   */
  organized_lints(text, language, all_headings, regex_mask, dedup, isolate_english) {
    const ptr0 = passStringToWasm0(text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    var ptr1 = isLikeNone(regex_mask) ? 0 : passStringToWasm0(regex_mask, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    var len1 = WASM_VECTOR_LEN;
    const ret = wasm.linter_organized_lints(this.__wbg_ptr, ptr0, len0, language, all_headings, ptr1, len1, dedup, isolate_english);
    var v3 = getArrayJsValueFromWasm0(ret[0], ret[1]);
    wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v3;
  }
  /**
   * @param {string} json
   */
  set_lint_config_from_json(json) {
    const ptr0 = passStringToWasm0(json, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.linter_set_lint_config_from_json(this.__wbg_ptr, ptr0, len0);
    if (ret[1]) {
      throw takeFromExternrefTable0(ret[0]);
    }
  }
  /**
   * @param {any} object
   */
  set_lint_config_from_object(object) {
    const ret = wasm.linter_set_lint_config_from_object(this.__wbg_ptr, object);
    if (ret[1]) {
      throw takeFromExternrefTable0(ret[0]);
    }
  }
  /**
   * @param {bigint | null} [start_time]
   * @param {bigint | null} [end_time]
   * @returns {any}
   */
  summarize_stats(start_time, end_time) {
    const ret = wasm.linter_summarize_stats(this.__wbg_ptr, !isLikeNone(start_time), isLikeNone(start_time) ? BigInt(0) : start_time, !isLikeNone(end_time), isLikeNone(end_time) ? BigInt(0) : end_time);
    return ret;
  }
}
if (Symbol.dispose) Linter2.prototype[Symbol.dispose] = Linter2.prototype.free;
class OrganizedGroup2 {
  static __wrap(ptr) {
    const obj = Object.create(OrganizedGroup2.prototype);
    obj.__wbg_ptr = ptr;
    OrganizedGroupFinalization.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    OrganizedGroupFinalization.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm.__wbg_organizedgroup_free(ptr, 0);
  }
  /**
   * @returns {string}
   */
  get group() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.__wbg_get_organizedgroup_group(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {Lint[]}
   */
  get lints() {
    const ret = wasm.__wbg_get_organizedgroup_lints(this.__wbg_ptr);
    var v1 = getArrayJsValueFromWasm0(ret[0], ret[1]);
    wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
    return v1;
  }
  /**
   * @param {string} arg0
   */
  set group(arg0) {
    const ptr0 = passStringToWasm0(arg0, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    wasm.__wbg_set_organizedgroup_group(this.__wbg_ptr, ptr0, len0);
  }
  /**
   * @param {Lint[]} arg0
   */
  set lints(arg0) {
    const ptr0 = passArrayJsValueToWasm0(arg0, wasm.__wbindgen_malloc);
    const len0 = WASM_VECTOR_LEN;
    wasm.__wbg_set_organizedgroup_lints(this.__wbg_ptr, ptr0, len0);
  }
}
if (Symbol.dispose) OrganizedGroup2.prototype[Symbol.dispose] = OrganizedGroup2.prototype.free;
class Span2 {
  static __wrap(ptr) {
    const obj = Object.create(Span2.prototype);
    obj.__wbg_ptr = ptr;
    SpanFinalization.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    SpanFinalization.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm.__wbg_span_free(ptr, 0);
  }
  /**
   * @returns {number}
   */
  get end() {
    const ret = wasm.__wbg_get_span_end(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @returns {number}
   */
  get start() {
    const ret = wasm.__wbg_get_span_start(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @param {number} arg0
   */
  set end(arg0) {
    wasm.__wbg_set_span_end(this.__wbg_ptr, arg0);
  }
  /**
   * @param {number} arg0
   */
  set start(arg0) {
    wasm.__wbg_set_span_start(this.__wbg_ptr, arg0);
  }
  /**
   * @param {string} json
   * @returns {Span}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0(json, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.span_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0(ret[1]);
    }
    return Span2.__wrap(ret[0]);
  }
  /**
   * @returns {boolean}
   */
  is_empty() {
    const ret = wasm.span_is_empty(this.__wbg_ptr);
    return ret !== 0;
  }
  /**
   * @returns {number}
   */
  len() {
    const ret = wasm.span_len(this.__wbg_ptr);
    return ret >>> 0;
  }
  /**
   * @param {number} start
   * @param {number} end
   * @returns {Span}
   */
  static new(start, end) {
    const ret = wasm.span_new(start, end);
    return Span2.__wrap(ret);
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.span_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
}
if (Symbol.dispose) Span2.prototype[Symbol.dispose] = Span2.prototype.free;
class Suggestion2 {
  static __wrap(ptr) {
    const obj = Object.create(Suggestion2.prototype);
    obj.__wbg_ptr = ptr;
    SuggestionFinalization.register(obj, obj.__wbg_ptr, obj);
    return obj;
  }
  __destroy_into_raw() {
    const ptr = this.__wbg_ptr;
    this.__wbg_ptr = 0;
    SuggestionFinalization.unregister(this);
    return ptr;
  }
  free() {
    const ptr = this.__destroy_into_raw();
    wasm.__wbg_suggestion_free(ptr, 0);
  }
  /**
   * @param {string} json
   * @returns {Suggestion}
   */
  static from_json(json) {
    const ptr0 = passStringToWasm0(json, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.suggestion_from_json(ptr0, len0);
    if (ret[2]) {
      throw takeFromExternrefTable0(ret[1]);
    }
    return Suggestion2.__wrap(ret[0]);
  }
  /**
   * Get the text that is going to replace the problematic section.
   * If [\`Self::kind\`] is \`SuggestionKind::Remove\`, this will return an empty
   * string.
   * @returns {string}
   */
  get_replacement_text() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.suggestion_get_replacement_text(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
  /**
   * @returns {SuggestionKind}
   */
  kind() {
    const ret = wasm.suggestion_kind(this.__wbg_ptr);
    return ret;
  }
  /**
   * @returns {string}
   */
  to_json() {
    let deferred1_0;
    let deferred1_1;
    try {
      const ret = wasm.suggestion_to_json(this.__wbg_ptr);
      deferred1_0 = ret[0];
      deferred1_1 = ret[1];
      return getStringFromWasm0(ret[0], ret[1]);
    } finally {
      wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
  }
}
if (Symbol.dispose) Suggestion2.prototype[Symbol.dispose] = Suggestion2.prototype.free;
const SuggestionKind = Object.freeze({
  /**
   * Replace the problematic text.
   */
  Replace: 0,
  "0": "Replace",
  /**
   * Remove the problematic text.
   */
  Remove: 1,
  "1": "Remove",
  /**
   * Insert additional text after the error.
   */
  InsertAfter: 2,
  "2": "InsertAfter"
});
function get_default_lint_config() {
  const ret = wasm.get_default_lint_config();
  return ret;
}
function get_default_lint_config_as_json() {
  let deferred1_0;
  let deferred1_1;
  try {
    const ret = wasm.get_default_lint_config_as_json();
    deferred1_0 = ret[0];
    deferred1_1 = ret[1];
    return getStringFromWasm0(ret[0], ret[1]);
  } finally {
    wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
  }
}
function setup() {
  wasm.setup();
}
function to_title_case(text) {
  let deferred2_0;
  let deferred2_1;
  try {
    const ptr0 = passStringToWasm0(text, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
    const len0 = WASM_VECTOR_LEN;
    const ret = wasm.to_title_case(ptr0, len0);
    deferred2_0 = ret[0];
    deferred2_1 = ret[1];
    return getStringFromWasm0(ret[0], ret[1]);
  } finally {
    wasm.__wbindgen_free(deferred2_0, deferred2_1, 1);
  }
}
function __wbg_get_imports() {
  const import0 = {
    __proto__: null,
    __wbg_Error_408e67f47ca7b58b: function(arg0, arg1) {
      const ret = Error(getStringFromWasm0(arg0, arg1));
      return ret;
    },
    __wbg_String_8564e559799eccda: function(arg0, arg1) {
      const ret = String(arg1);
      const ptr1 = passStringToWasm0(ret, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN;
      getDataViewMemory0().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_boolean_get_c9c83ebd41b34df3: function(arg0) {
      const v = arg0;
      const ret = typeof v === "boolean" ? v : void 0;
      return isLikeNone(ret) ? 16777215 : ret ? 1 : 0;
    },
    __wbg___wbindgen_debug_string_a57024b9c6e4a48b: function(arg0, arg1) {
      const ret = debugString(arg1);
      const ptr1 = passStringToWasm0(ret, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN;
      getDataViewMemory0().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_is_function_5e4570eb24ffa122: function(arg0) {
      const ret = typeof arg0 === "function";
      return ret;
    },
    __wbg___wbindgen_is_object_a2790eb24c211ea0: function(arg0) {
      const val = arg0;
      const ret = typeof val === "object" && val !== null;
      return ret;
    },
    __wbg___wbindgen_is_string_e6f02f0ea5f20a32: function(arg0) {
      const ret = typeof arg0 === "string";
      return ret;
    },
    __wbg___wbindgen_jsval_loose_eq_acf2776254a8d832: function(arg0, arg1) {
      const ret = arg0 == arg1;
      return ret;
    },
    __wbg___wbindgen_number_get_136b9679cab35cfb: function(arg0, arg1) {
      const obj = arg1;
      const ret = typeof obj === "number" ? obj : void 0;
      getDataViewMemory0().setFloat64(arg0 + 8 * 1, isLikeNone(ret) ? 0 : ret, true);
      getDataViewMemory0().setInt32(arg0 + 4 * 0, !isLikeNone(ret), true);
    },
    __wbg___wbindgen_string_get_d154f1e671052120: function(arg0, arg1) {
      const obj = arg1;
      const ret = typeof obj === "string" ? obj : void 0;
      var ptr1 = isLikeNone(ret) ? 0 : passStringToWasm0(ret, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      var len1 = WASM_VECTOR_LEN;
      getDataViewMemory0().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg___wbindgen_throw_bb96b2010945f0bc: function(arg0, arg1) {
      throw new Error(getStringFromWasm0(arg0, arg1));
    },
    __wbg_call_1c5886ab9c57d1c7: function() {
      return handleError(function(arg0, arg1) {
        const ret = arg0.call(arg1);
        return ret;
      }, arguments);
    },
    __wbg_done_669171204c3dcae2: function(arg0) {
      const ret = arg0.done;
      return ret;
    },
    __wbg_entries_7774d489e1da5f4f: function(arg0) {
      const ret = Object.entries(arg0);
      return ret;
    },
    __wbg_error_757e9472f8410341: function(arg0, arg1) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.error(getStringFromWasm0(arg0, arg1));
      } finally {
        wasm.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_getRandomValues_a608c4436c19407a: function() {
      return handleError(function(arg0, arg1) {
        globalThis.crypto.getRandomValues(getArrayU8FromWasm0(arg0, arg1));
      }, arguments);
    },
    __wbg_getRandomValues_e446ea5ffdd14ee5: function() {
      return handleError(function(arg0, arg1) {
        globalThis.crypto.getRandomValues(getArrayU8FromWasm0(arg0, arg1));
      }, arguments);
    },
    __wbg_getTime_63fb0332e6c4ec17: function(arg0) {
      const ret = arg0.getTime();
      return ret;
    },
    __wbg_get_c0c8f8d7da0c03dd: function(arg0, arg1) {
      const ret = arg0[arg1 >>> 0];
      return ret;
    },
    __wbg_get_d173c0308df22d37: function() {
      return handleError(function(arg0, arg1) {
        const ret = Reflect.get(arg0, arg1);
        return ret;
      }, arguments);
    },
    __wbg_get_unchecked_e20b893aeafc3fca: function(arg0, arg1) {
      const ret = arg0[arg1 >>> 0];
      return ret;
    },
    __wbg_instanceof_ArrayBuffer_993d02d2d254cad1: function(arg0) {
      let result;
      try {
        result = arg0 instanceof ArrayBuffer;
      } catch (_) {
        result = false;
      }
      const ret = result;
      return ret;
    },
    __wbg_instanceof_Uint8Array_f935dbb0aa7cdeed: function(arg0) {
      let result;
      try {
        result = arg0 instanceof Uint8Array;
      } catch (_) {
        result = false;
      }
      const ret = result;
      return ret;
    },
    __wbg_iterator_5cebbb86e33c6dd6: function() {
      const ret = Symbol.iterator;
      return ret;
    },
    __wbg_length_36bd29c6848c2144: function(arg0) {
      const ret = arg0.length;
      return ret;
    },
    __wbg_length_ecfa2c63d3d0d82c: function(arg0) {
      const ret = arg0.length;
      return ret;
    },
    __wbg_lint_new: function(arg0) {
      const ret = Lint2.__wrap(arg0);
      return ret;
    },
    __wbg_lint_unwrap: function(arg0) {
      const ret = Lint2.__unwrap(arg0);
      return ret;
    },
    __wbg_log_1f8cbb01c83d06c2: function(arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.log(getStringFromWasm0(arg0, arg1), getStringFromWasm0(arg2, arg3), getStringFromWasm0(arg4, arg5), getStringFromWasm0(arg6, arg7));
      } finally {
        wasm.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_log_a54ca6b45e09078a: function(arg0, arg1) {
      let deferred0_0;
      let deferred0_1;
      try {
        deferred0_0 = arg0;
        deferred0_1 = arg1;
        console.log(getStringFromWasm0(arg0, arg1));
      } finally {
        wasm.__wbindgen_free(deferred0_0, deferred0_1, 1);
      }
    },
    __wbg_mark_6b7f03786f5e4d61: function(arg0, arg1) {
      performance.mark(getStringFromWasm0(arg0, arg1));
    },
    __wbg_measure_0e21b33a1c6e3a29: function() {
      return handleError(function(arg0, arg1, arg2, arg3) {
        let deferred0_0;
        let deferred0_1;
        let deferred1_0;
        let deferred1_1;
        try {
          deferred0_0 = arg0;
          deferred0_1 = arg1;
          deferred1_0 = arg2;
          deferred1_1 = arg3;
          performance.measure(getStringFromWasm0(arg0, arg1), getStringFromWasm0(arg2, arg3));
        } finally {
          wasm.__wbindgen_free(deferred0_0, deferred0_1, 1);
          wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
      }, arguments);
    },
    __wbg_new_0_f117d868b403dc07: function() {
      const ret = /* @__PURE__ */ new Date();
      return ret;
    },
    __wbg_new_116be93542d39019: function() {
      const ret = new Array();
      return ret;
    },
    __wbg_new_227d7c05414eb861: function() {
      const ret = new Error();
      return ret;
    },
    __wbg_new_77cc4f4f472aeb81: function(arg0) {
      const ret = new Uint8Array(arg0);
      return ret;
    },
    __wbg_new_cdf041679ded4c5f: function() {
      const ret = /* @__PURE__ */ new Map();
      return ret;
    },
    __wbg_new_ebe3e0f6837f0879: function() {
      const ret = new Object();
      return ret;
    },
    __wbg_next_42cf16ee0dafc9e2: function() {
      return handleError(function(arg0) {
        const ret = arg0.next();
        return ret;
      }, arguments);
    },
    __wbg_next_8f26b64fa5e9f64b: function(arg0) {
      const ret = arg0.next;
      return ret;
    },
    __wbg_organizedgroup_new: function(arg0) {
      const ret = OrganizedGroup2.__wrap(arg0);
      return ret;
    },
    __wbg_prototypesetcall_de8e0d9553586985: function(arg0, arg1, arg2) {
      Uint8Array.prototype.set.call(getArrayU8FromWasm0(arg0, arg1), arg2);
    },
    __wbg_set_014226dfeca53178: function(arg0, arg1, arg2) {
      const ret = arg0.set(arg1, arg2);
      return ret;
    },
    __wbg_set_6be42768c690e380: function(arg0, arg1, arg2) {
      arg0[arg1] = arg2;
    },
    __wbg_set_a80955eb93b145c6: function(arg0, arg1, arg2) {
      arg0[arg1 >>> 0] = arg2;
    },
    __wbg_stack_3b0d974bbf31e44f: function(arg0, arg1) {
      const ret = arg1.stack;
      const ptr1 = passStringToWasm0(ret, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
      const len1 = WASM_VECTOR_LEN;
      getDataViewMemory0().setInt32(arg0 + 4 * 1, len1, true);
      getDataViewMemory0().setInt32(arg0 + 4 * 0, ptr1, true);
    },
    __wbg_suggestion_new: function(arg0) {
      const ret = Suggestion2.__wrap(arg0);
      return ret;
    },
    __wbg_value_1e2369fab29b420e: function(arg0) {
      const ret = arg0.value;
      return ret;
    },
    __wbindgen_cast_0000000000000001: function(arg0) {
      const ret = arg0;
      return ret;
    },
    __wbindgen_cast_0000000000000002: function(arg0, arg1) {
      const ret = getStringFromWasm0(arg0, arg1);
      return ret;
    },
    __wbindgen_init_externref_table: function() {
      const table = wasm.__wbindgen_externrefs;
      const offset = table.grow(4);
      table.set(0, void 0);
      table.set(offset + 0, void 0);
      table.set(offset + 1, null);
      table.set(offset + 2, true);
      table.set(offset + 3, false);
    }
  };
  return {
    __proto__: null,
    "./harper_wasm_bg.js": import0
  };
}
const LintFinalization = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm.__wbg_lint_free(ptr, 1));
const LinterFinalization = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm.__wbg_linter_free(ptr, 1));
const OrganizedGroupFinalization = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm.__wbg_organizedgroup_free(ptr, 1));
const SpanFinalization = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm.__wbg_span_free(ptr, 1));
const SuggestionFinalization = typeof FinalizationRegistry === "undefined" ? { register: () => {
}, unregister: () => {
} } : new FinalizationRegistry((ptr) => wasm.__wbg_suggestion_free(ptr, 1));
function addToExternrefTable0(obj) {
  const idx = wasm.__externref_table_alloc();
  wasm.__wbindgen_externrefs.set(idx, obj);
  return idx;
}
function _assertClass(instance, klass) {
  if (!(instance instanceof klass)) {
    throw new Error(\`expected instance of \${klass.name}\`);
  }
}
function debugString(val) {
  const type = typeof val;
  if (type == "number" || type == "boolean" || val == null) {
    return \`\${val}\`;
  }
  if (type == "string") {
    return \`"\${val}"\`;
  }
  if (type == "symbol") {
    const description = val.description;
    if (description == null) {
      return "Symbol";
    } else {
      return \`Symbol(\${description})\`;
    }
  }
  if (type == "function") {
    const name = val.name;
    if (typeof name == "string" && name.length > 0) {
      return \`Function(\${name})\`;
    } else {
      return "Function";
    }
  }
  if (Array.isArray(val)) {
    const length = val.length;
    let debug = "[";
    if (length > 0) {
      debug += debugString(val[0]);
    }
    for (let i = 1; i < length; i++) {
      debug += ", " + debugString(val[i]);
    }
    debug += "]";
    return debug;
  }
  const builtInMatches = /\\[object ([^\\]]+)\\]/.exec(toString.call(val));
  let className;
  if (builtInMatches && builtInMatches.length > 1) {
    className = builtInMatches[1];
  } else {
    return toString.call(val);
  }
  if (className == "Object") {
    try {
      return "Object(" + JSON.stringify(val) + ")";
    } catch (_) {
      return "Object";
    }
  }
  if (val instanceof Error) {
    return \`\${val.name}: \${val.message}
\${val.stack}\`;
  }
  return className;
}
function getArrayJsValueFromWasm0(ptr, len) {
  ptr = ptr >>> 0;
  const mem = getDataViewMemory0();
  const result = [];
  for (let i = ptr; i < ptr + 4 * len; i += 4) {
    result.push(wasm.__wbindgen_externrefs.get(mem.getUint32(i, true)));
  }
  wasm.__externref_drop_slice(ptr, len);
  return result;
}
function getArrayU8FromWasm0(ptr, len) {
  ptr = ptr >>> 0;
  return getUint8ArrayMemory0().subarray(ptr / 1, ptr / 1 + len);
}
let cachedBigUint64ArrayMemory0 = null;
function getBigUint64ArrayMemory0() {
  if (cachedBigUint64ArrayMemory0 === null || cachedBigUint64ArrayMemory0.byteLength === 0) {
    cachedBigUint64ArrayMemory0 = new BigUint64Array(wasm.memory.buffer);
  }
  return cachedBigUint64ArrayMemory0;
}
let cachedDataViewMemory0 = null;
function getDataViewMemory0() {
  if (cachedDataViewMemory0 === null || cachedDataViewMemory0.buffer.detached === true || cachedDataViewMemory0.buffer.detached === void 0 && cachedDataViewMemory0.buffer !== wasm.memory.buffer) {
    cachedDataViewMemory0 = new DataView(wasm.memory.buffer);
  }
  return cachedDataViewMemory0;
}
function getStringFromWasm0(ptr, len) {
  return decodeText(ptr >>> 0, len);
}
let cachedUint8ArrayMemory0 = null;
function getUint8ArrayMemory0() {
  if (cachedUint8ArrayMemory0 === null || cachedUint8ArrayMemory0.byteLength === 0) {
    cachedUint8ArrayMemory0 = new Uint8Array(wasm.memory.buffer);
  }
  return cachedUint8ArrayMemory0;
}
function handleError(f, args) {
  try {
    return f.apply(this, args);
  } catch (e) {
    const idx = addToExternrefTable0(e);
    wasm.__wbindgen_exn_store(idx);
  }
}
function isLikeNone(x) {
  return x === void 0 || x === null;
}
function passArray64ToWasm0(arg, malloc) {
  const ptr = malloc(arg.length * 8, 8) >>> 0;
  getBigUint64ArrayMemory0().set(arg, ptr / 8);
  WASM_VECTOR_LEN = arg.length;
  return ptr;
}
function passArray8ToWasm0(arg, malloc) {
  const ptr = malloc(arg.length * 1, 1) >>> 0;
  getUint8ArrayMemory0().set(arg, ptr / 1);
  WASM_VECTOR_LEN = arg.length;
  return ptr;
}
function passArrayJsValueToWasm0(array, malloc) {
  const ptr = malloc(array.length * 4, 4) >>> 0;
  for (let i = 0; i < array.length; i++) {
    const add = addToExternrefTable0(array[i]);
    getDataViewMemory0().setUint32(ptr + 4 * i, add, true);
  }
  WASM_VECTOR_LEN = array.length;
  return ptr;
}
function passStringToWasm0(arg, malloc, realloc) {
  if (realloc === void 0) {
    const buf = cachedTextEncoder.encode(arg);
    const ptr2 = malloc(buf.length, 1) >>> 0;
    getUint8ArrayMemory0().subarray(ptr2, ptr2 + buf.length).set(buf);
    WASM_VECTOR_LEN = buf.length;
    return ptr2;
  }
  let len = arg.length;
  let ptr = malloc(len, 1) >>> 0;
  const mem = getUint8ArrayMemory0();
  let offset = 0;
  for (; offset < len; offset++) {
    const code = arg.charCodeAt(offset);
    if (code > 127) break;
    mem[ptr + offset] = code;
  }
  if (offset !== len) {
    if (offset !== 0) {
      arg = arg.slice(offset);
    }
    ptr = realloc(ptr, len, len = offset + arg.length * 3, 1) >>> 0;
    const view = getUint8ArrayMemory0().subarray(ptr + offset, ptr + len);
    const ret = cachedTextEncoder.encodeInto(arg, view);
    offset += ret.written;
    ptr = realloc(ptr, len, offset, 1) >>> 0;
  }
  WASM_VECTOR_LEN = offset;
  return ptr;
}
function takeFromExternrefTable0(idx) {
  const value = wasm.__wbindgen_externrefs.get(idx);
  wasm.__externref_table_dealloc(idx);
  return value;
}
let cachedTextDecoder = new TextDecoder("utf-8", { ignoreBOM: true, fatal: true });
cachedTextDecoder.decode();
const MAX_SAFARI_DECODE_BYTES = 2146435072;
let numBytesDecoded = 0;
function decodeText(ptr, len) {
  numBytesDecoded += len;
  if (numBytesDecoded >= MAX_SAFARI_DECODE_BYTES) {
    cachedTextDecoder = new TextDecoder("utf-8", { ignoreBOM: true, fatal: true });
    cachedTextDecoder.decode();
    numBytesDecoded = len;
  }
  return cachedTextDecoder.decode(getUint8ArrayMemory0().subarray(ptr, ptr + len));
}
const cachedTextEncoder = new TextEncoder();
if (!("encodeInto" in cachedTextEncoder)) {
  cachedTextEncoder.encodeInto = function(arg, view) {
    const buf = cachedTextEncoder.encode(arg);
    view.set(buf);
    return {
      read: arg.length,
      written: buf.length
    };
  };
}
let WASM_VECTOR_LEN = 0;
let wasm;
function __wbg_finalize_init(instance, module) {
  wasm = instance.exports;
  cachedBigUint64ArrayMemory0 = null;
  cachedDataViewMemory0 = null;
  cachedUint8ArrayMemory0 = null;
  wasm.__wbindgen_start();
  return wasm;
}
async function __wbg_load(module, imports) {
  if (typeof Response === "function" && module instanceof Response) {
    if (!module.ok) {
      throw new Error(\`failed to fetch Wasm: \${module.status} \${module.statusText} fetching '\${module.url}'\`);
    }
    if (typeof WebAssembly.instantiateStreaming === "function") {
      try {
        return await WebAssembly.instantiateStreaming(module, imports);
      } catch (e) {
        const validResponse = expectedResponseType(module.type);
        if (validResponse && module.headers.get("Content-Type") !== "application/wasm") {
          console.warn("\`WebAssembly.instantiateStreaming\` failed because your server does not serve Wasm with \`application/wasm\` MIME type. Falling back to \`WebAssembly.instantiate\` which is slower. Original error:\\n", e);
        } else {
          throw e;
        }
      }
    }
    const bytes = await module.arrayBuffer();
    return await WebAssembly.instantiate(bytes, imports);
  } else {
    const instance = await WebAssembly.instantiate(module, imports);
    if (instance instanceof WebAssembly.Instance) {
      return { instance, module };
    } else {
      return instance;
    }
  }
  function expectedResponseType(type) {
    switch (type) {
      case "basic":
      case "cors":
      case "default":
        return true;
    }
    return false;
  }
}
function initSync(module) {
  if (wasm !== void 0) return wasm;
  if (module !== void 0) {
    if (Object.getPrototypeOf(module) === Object.prototype) {
      ({ module } = module);
    } else {
      console.warn("using deprecated parameters for \`initSync()\`; pass a single object instead");
    }
  }
  const imports = __wbg_get_imports();
  if (!(module instanceof WebAssembly.Module)) {
    module = new WebAssembly.Module(module);
  }
  const instance = new WebAssembly.Instance(module, imports);
  return __wbg_finalize_init(instance);
}
async function __wbg_init(module_or_path) {
  if (wasm !== void 0) return wasm;
  if (module_or_path !== void 0) {
    if (Object.getPrototypeOf(module_or_path) === Object.prototype) {
      ({ module_or_path } = module_or_path);
    } else {
      console.warn("using deprecated parameters for the initialization function; pass a single object instead");
    }
  }
  if (module_or_path === void 0) {
    module_or_path = new URL();
  }
  const imports = __wbg_get_imports();
  if (typeof module_or_path === "string" || typeof Request === "function" && module_or_path instanceof Request || typeof URL === "function" && module_or_path instanceof URL) {
    module_or_path = fetch(module_or_path);
  }
  const { instance, module } = await __wbg_load(await module_or_path, imports);
  return __wbg_finalize_init(instance);
}
var fullGlue = /* @__PURE__ */ Object.freeze({
  __proto__: null,
  Dialect,
  Language,
  Lint: Lint2,
  Linter: Linter2,
  OrganizedGroup: OrganizedGroup2,
  Span: Span2,
  Suggestion: Suggestion2,
  SuggestionKind,
  default: __wbg_init,
  get_default_lint_config,
  get_default_lint_config_as_json,
  initSync,
  setup,
  to_title_case
});
const _PLazy = class _PLazy extends Promise {
  constructor(executor) {
    super((resolve) => {
      resolve();
    });
    __privateAdd(this, _executor);
    __privateAdd(this, _promise);
    __privateSet(this, _executor, executor);
  }
  static from(function_) {
    return new _PLazy((resolve) => {
      resolve(function_());
    });
  }
  static resolve(value) {
    return new _PLazy((resolve) => {
      resolve(value);
    });
  }
  static reject(error) {
    return new _PLazy((resolve, reject) => {
      reject(error);
    });
  }
  then(onFulfilled, onRejected) {
    __privateGet(this, _promise) ?? __privateSet(this, _promise, new Promise(__privateGet(this, _executor)));
    return __privateGet(this, _promise).then(onFulfilled, onRejected);
  }
  catch(onRejected) {
    __privateGet(this, _promise) ?? __privateSet(this, _promise, new Promise(__privateGet(this, _executor)));
    return __privateGet(this, _promise).catch(onRejected);
  }
  finally(onFinally) {
    __privateGet(this, _promise) ?? __privateSet(this, _promise, new Promise(__privateGet(this, _executor)));
    return __privateGet(this, _promise).finally(onFinally);
  }
};
_executor = new WeakMap();
_promise = new WeakMap();
let PLazy = _PLazy;
const copyProperty = (to, from, property, ignoreNonConfigurable) => {
  if (property === "length" || property === "prototype") {
    return;
  }
  if (property === "arguments" || property === "caller") {
    return;
  }
  const toDescriptor = Object.getOwnPropertyDescriptor(to, property);
  const fromDescriptor = Object.getOwnPropertyDescriptor(from, property);
  if (!canCopyProperty(toDescriptor, fromDescriptor) && ignoreNonConfigurable) {
    return;
  }
  Object.defineProperty(to, property, fromDescriptor);
};
const canCopyProperty = function(toDescriptor, fromDescriptor) {
  return toDescriptor === void 0 || toDescriptor.configurable || toDescriptor.writable === fromDescriptor.writable && toDescriptor.enumerable === fromDescriptor.enumerable && toDescriptor.configurable === fromDescriptor.configurable && (toDescriptor.writable || toDescriptor.value === fromDescriptor.value);
};
const changePrototype = (to, from) => {
  const fromPrototype = Object.getPrototypeOf(from);
  if (fromPrototype === Object.getPrototypeOf(to)) {
    return;
  }
  Object.setPrototypeOf(to, fromPrototype);
};
const wrappedToString = (withName, fromBody) => \`/* Wrapped \${withName}*/
\${fromBody}\`;
const toStringDescriptor = Object.getOwnPropertyDescriptor(Function.prototype, "toString");
const toStringName = Object.getOwnPropertyDescriptor(Function.prototype.toString, "name");
const changeToString = (to, from, name) => {
  const withName = name === "" ? "" : \`with \${name.trim()}() \`;
  const newToString = wrappedToString.bind(null, withName, from.toString());
  Object.defineProperty(newToString, "name", toStringName);
  Object.defineProperty(to, "toString", { ...toStringDescriptor, value: newToString });
};
function mimicFunction(to, from, { ignoreNonConfigurable = false } = {}) {
  const { name } = to;
  for (const property of Reflect.ownKeys(from)) {
    copyProperty(to, from, property, ignoreNonConfigurable);
  }
  changePrototype(to, from);
  changeToString(to, from, name);
  return to;
}
const cacheStore = /* @__PURE__ */ new WeakMap();
function pMemoize(fn, { cacheKey = ([firstArgument]) => firstArgument, cache = /* @__PURE__ */ new Map() } = {}) {
  const promiseCache = /* @__PURE__ */ new Map();
  const memoized = function(...arguments_) {
    const key = cacheKey(arguments_);
    if (promiseCache.has(key)) {
      return promiseCache.get(key);
    }
    const promise = (async () => {
      try {
        if (cache && await cache.has(key)) {
          return await cache.get(key);
        }
        const promise2 = fn.apply(this, arguments_);
        const result = await promise2;
        try {
          return result;
        } finally {
          if (cache) {
            await cache.set(key, result);
          }
        }
      } finally {
        promiseCache.delete(key);
      }
    })();
    promiseCache.set(key, promise);
    return promise;
  };
  mimicFunction(memoized, fn, {
    ignoreNonConfigurable: true
  });
  cacheStore.set(memoized, cache);
  return memoized;
}
function inferGlueFlavor(binary) {
  return binary.includes("harper_wasm_slim") ? "slim" : "full";
}
function loadGlue(glueFlavor) {
  if (glueFlavor === "slim") {
    return defaultGlue;
  }
  return fullGlue;
}
function getDefaultGlueBinary(binary, glueFlavor) {
  if (glueFlavor === "slim") {
    return binary;
  }
  if (binary.includes("harper_wasm_bg.wasm")) {
    return binary.replace("harper_wasm_bg.wasm", "harper_wasm_slim_bg.wasm");
  }
  return null;
}
function getInitInput(binary) {
  if (typeof process !== "undefined" && binary.startsWith("file://")) {
    return Promise.resolve().then(function() {
      return __viteBrowserExternal$1;
    }).then(
      (fs) => new Promise((resolve, reject) => {
        fs.readFile(new URL(binary).pathname, (err, data) => {
          if (err) reject(err);
          resolve(data);
        });
      })
    );
  }
  return binary;
}
async function loadBinaryUncached(binary, glueFlavor) {
  const exports = loadGlue(glueFlavor);
  const defaultGlueBinary = getDefaultGlueBinary(binary, glueFlavor);
  if (defaultGlueBinary != null) {
    try {
      await __wbg_init$1({ module_or_path: getInitInput(defaultGlueBinary) });
    } catch (err) {
      if (glueFlavor === "slim") {
        throw err;
      }
    }
  }
  await exports.default({ module_or_path: getInitInput(binary) });
  return exports;
}
const loadBinaryByFlavor = {
  full: pMemoize((binary) => loadBinaryUncached(binary, "full")),
  slim: pMemoize((binary) => loadBinaryUncached(binary, "slim"))
};
function loadBinary(binary, glueFlavor) {
  return loadBinaryByFlavor[glueFlavor](binary);
}
class BinaryModuleImpl {
  constructor() {
    __publicField(this, "url", "");
    __publicField(this, "glueFlavor", "full");
    __publicField(this, "inner", null);
  }
  /** Load a binary from a specified URL. This is the only recommended way to construct this type. */
  static create(url, glueFlavor) {
    const module = new SuperBinaryModule();
    module.url = url;
    module.glueFlavor = glueFlavor ?? inferGlueFlavor(typeof url === "string" ? url : url.href);
    module.inner = PLazy.from(
      () => loadBinary(typeof module.url === "string" ? module.url : module.url.href, module.glueFlavor)
    );
    return module;
  }
  async getDefaultLintConfigAsJSON() {
    const exported = await this.inner;
    return exported.get_default_lint_config_as_json();
  }
  async getDefaultLintConfig() {
    const exported = await this.inner;
    return exported.get_default_lint_config();
  }
  async toTitleCase(text) {
    const exported = await this.inner;
    return exported.to_title_case(text);
  }
  async setup() {
    const exported = await this.inner;
    exported.setup();
  }
}
class SuperBinaryModule extends BinaryModuleImpl {
  async createLinter(dialect) {
    const exported = await this.getBinaryModule();
    return exported.Linter.new(dialect ?? Dialect$1.American);
  }
  async getBinaryModule() {
    return await PLazy.from(
      () => loadBinary(typeof this.url === "string" ? this.url : this.url.href, this.glueFlavor)
    );
  }
}
function toWasmLanguage(language) {
  switch (language) {
    case "plaintext":
      return Language$1.Plain;
    case "typst":
      return Language$1.Typst;
    case "markdown":
    case void 0:
      return Language$1.Markdown;
    default:
      console.warn(\`Unknown Harper language '\${String(language)}'; using markdown.\`);
      return Language$1.Markdown;
  }
}
function toWasmLintArgs(text, options) {
  return [
    text,
    toWasmLanguage(options == null ? void 0 : options.language),
    (options == null ? void 0 : options.forceAllHeadings) ?? false,
    options == null ? void 0 : options.regex_mask,
    (options == null ? void 0 : options.dedup) ?? true,
    (options == null ? void 0 : options.isolateEnglish) ?? false
  ];
}
class LocalLinter {
  constructor(init) {
    __publicField(this, "binary");
    __publicField(this, "inner");
    __publicField(this, "disposed", false);
    this.binary = init.binary;
    this.binary.setup();
    this.inner = this.createInner(init.dialect);
  }
  createInner(dialect) {
    return PLazy.from(async () => {
      await this.binary.setup();
      return this.binary.createLinter(dialect);
    });
  }
  async setup() {
    await this.lint("", { language: "plaintext" });
    const exported = await this.exportIgnoredLints();
    await this.importIgnoredLints(exported);
  }
  async lint(text, options) {
    const inner = await this.inner;
    return inner.lint(...toWasmLintArgs(text, options));
  }
  async organizedLints(text, options) {
    const inner = await this.inner;
    const lintGroups = inner.organized_lints(...toWasmLintArgs(text, options));
    const output = {};
    for (const group of lintGroups) {
      output[group.group] = group.lints;
      group.free();
    }
    return output;
  }
  async applySuggestion(text, lint, suggestion) {
    const inner = await this.inner;
    return inner.apply_suggestion(text, lint, suggestion);
  }
  async isLikelyEnglish(text) {
    const inner = await this.inner;
    return inner.is_likely_english(text);
  }
  async isolateEnglish(text) {
    const inner = await this.inner;
    return inner.isolate_english(text);
  }
  async getLintConfig() {
    const inner = await this.inner;
    return inner.get_lint_config_as_object();
  }
  async getDefaultLintConfigAsJSON() {
    return await this.binary.getDefaultLintConfigAsJSON();
  }
  async getDefaultLintConfig() {
    return await this.binary.getDefaultLintConfig();
  }
  async getStructuredLintConfig() {
    const inner = await this.inner;
    return inner.get_structured_lint_config_as_object();
  }
  async getStructuredLintConfigJSON() {
    const inner = await this.inner;
    return inner.get_structured_lint_config_as_json();
  }
  async setLintConfig(config) {
    const inner = await this.inner;
    inner.set_lint_config_from_object(config);
  }
  async getLintConfigAsJSON() {
    const inner = await this.inner;
    return inner.get_lint_config_as_json();
  }
  async setLintConfigWithJSON(config) {
    const inner = await this.inner;
    inner.set_lint_config_from_json(config);
  }
  async toTitleCase(text) {
    return await this.binary.toTitleCase(text);
  }
  async getLintDescriptions() {
    const inner = await this.inner;
    return inner.get_lint_descriptions_as_object();
  }
  async getLintDescriptionsAsJSON() {
    const inner = await this.inner;
    return inner.get_lint_descriptions_as_json();
  }
  async getLintDescriptionsHTML() {
    const inner = await this.inner;
    return inner.get_lint_descriptions_html_as_object();
  }
  async getLintDescriptionsHTMLAsJSON() {
    const inner = await this.inner;
    return inner.get_lint_descriptions_html_as_json();
  }
  async ignoreLint(source, lint) {
    return await this.ignoreLints(source, [lint]);
  }
  async ignoreLints(source, lints) {
    const inner = await this.inner;
    inner.ignore_lints(source, lints);
  }
  async ignoreLintHash(hash) {
    const inner = await this.inner;
    inner.ignore_hashes(new BigUint64Array([hash]));
  }
  async exportIgnoredLints() {
    const inner = await this.inner;
    return inner.export_ignored_lints();
  }
  async importIgnoredLints(json) {
    const inner = await this.inner;
    inner.import_ignored_lints(json);
  }
  async contextHash(source, lint) {
    const inner = await this.inner;
    return inner.context_hash(source, lint);
  }
  async clearIgnoredLints() {
    const inner = await this.inner;
    inner.clear_ignored_lints();
  }
  async clearWords() {
    const inner = await this.inner;
    return inner.clear_words();
  }
  async importWords(words) {
    const inner = await this.inner;
    return inner.import_words(words);
  }
  async exportWords() {
    const inner = await this.inner;
    return inner.export_words();
  }
  async getDialect() {
    const inner = await this.inner;
    return inner.get_dialect();
  }
  async setDialect(dialect) {
    const inner = await this.inner;
    if (inner.get_dialect() !== dialect) {
      inner.free();
      this.inner = this.createInner(dialect);
    }
    return Promise.resolve();
  }
  async summarizeStats(start, end) {
    const inner = await this.inner;
    return inner.summarize_stats(start, end);
  }
  async generateStatsFile() {
    const inner = await this.inner;
    return inner.generate_stats_file();
  }
  async importStatsFile(statsFile) {
    const inner = await this.inner;
    return inner.import_stats_file(statsFile);
  }
  /**
   * Load a Weirpack from a Blob.
   *
   * Returns \`undefined\` if tests pass and rules are imported, otherwise returns
   * the Weirpack test failures.
   */
  async loadWeirpackFromBlob(blob) {
    const bytes = new Uint8Array(await blob.arrayBuffer());
    return this.loadWeirpackFromBytes(bytes);
  }
  /**
   * Load a Weirpack from a byte array.
   *
   * Returns \`undefined\` if tests pass and rules are imported, otherwise returns
   * the Weirpack test failures.
   */
  async loadWeirpackFromBytes(bytes) {
    const inner = await this.inner;
    const data = bytes instanceof Uint8Array ? bytes : Uint8Array.from(bytes);
    const result = inner.import_weirpack(data);
    return result;
  }
  async dispose() {
    if (this.disposed) {
      return;
    }
    this.disposed = true;
    const inner = await this.inner;
    inner.free();
  }
}
function assert(condition, message) {
  if (!condition) {
    throw new Error("Assertion failed");
  }
}
function isSerializedRequest(v) {
  return typeof v === "object" && v !== null && "procName" in v && "args" in v;
}
class Serializer {
  constructor(binary) {
    __publicField(this, "binary");
    this.binary = binary;
    this.binary.setup();
  }
  async serializeArg(arg) {
    var _a;
    const { Lint: Lint3, Span: Span3, Suggestion: Suggestion3 } = await this.binary.getBinaryModule();
    if (Array.isArray(arg)) {
      return {
        json: JSON.stringify(await Promise.all(arg.map((a) => this.serializeArg(a)))),
        type: "Array"
      };
    }
    const argType = typeof arg;
    switch (argType) {
      case "string":
      case "number":
      case "boolean":
      case "undefined":
        return { json: JSON.stringify(arg), type: argType };
      case "bigint":
        return { json: arg.toString(), type: argType };
    }
    if (arg.to_json !== void 0) {
      const json = arg.to_json();
      let type;
      const constructorName = (_a = arg.constructor) == null ? void 0 : _a.name;
      if (arg instanceof Lint3 || constructorName === "Lint") {
        type = "Lint";
      } else if (arg instanceof Suggestion3 || constructorName === "Suggestion") {
        type = "Suggestion";
      } else if (arg instanceof Span3 || constructorName === "Span") {
        type = "Span";
      }
      if (type === void 0) {
        throw new Error("Unhandled case: type undefined");
      }
      return { json, type };
    }
    if (argType == "object") {
      return {
        json: JSON.stringify(
          await Promise.all(
            Object.entries(arg).map(([key, value]) => this.serializeArg([key, value]))
          )
        ),
        type: "object"
      };
    }
    throw new Error(\`Unhandled case: \${arg}\`);
  }
  async serialize(req) {
    return {
      procName: req.procName,
      args: await Promise.all(req.args.map((arg) => this.serializeArg(arg)))
    };
  }
  async deserializeArg(requestArg) {
    const { Lint: Lint3, Span: Span3, Suggestion: Suggestion3 } = await this.binary.getBinaryModule();
    switch (requestArg.type) {
      case "bigint":
        return BigInt(requestArg.json);
      case "undefined":
        return void 0;
      case "boolean":
      case "number":
      case "string":
        return JSON.parse(requestArg.json);
      case "Suggestion":
        return Suggestion3.from_json(requestArg.json);
      case "Lint":
        return Lint3.from_json(requestArg.json);
      case "Span":
        return Span3.from_json(requestArg.json);
      case "Array": {
        const parsed = JSON.parse(requestArg.json);
        assert(Array.isArray(parsed));
        return await Promise.all(parsed.map((arg) => this.deserializeArg(arg)));
      }
      case "object": {
        const parsed = JSON.parse(requestArg.json);
        return Object.fromEntries(
          await Promise.all(parsed.map((val) => this.deserializeArg(val)))
        );
      }
      default:
        throw new Error(\`Unhandled case: \${requestArg.type}\`);
    }
  }
  async deserialize(request) {
    return {
      procName: request.procName,
      args: await Promise.all(request.args.map((arg) => this.deserializeArg(arg)))
    };
  }
}
self.postMessage("ready");
self.onmessage = (e) => {
  const [binaryUrl, dialect, glueFlavor] = e.data;
  if (typeof binaryUrl !== "string") {
    throw new TypeError(\`Expected binary to be a string of url but got \${typeof binaryUrl}.\`);
  }
  if (glueFlavor !== void 0 && glueFlavor !== "full" && glueFlavor !== "slim") {
    throw new TypeError(\`Expected glue flavor to be "full" or "slim" but got \${glueFlavor}.\`);
  }
  const binary = SuperBinaryModule.create(binaryUrl, glueFlavor);
  const serializer = new Serializer(binary);
  const linter = new LocalLinter({ binary, dialect });
  async function processRequest(v) {
    const { procName, args } = await serializer.deserialize(v);
    if (procName in linter) {
      const res = await linter[procName](...args);
      postMessage(await serializer.serializeArg(res));
    }
  }
  self.onmessage = (e2) => {
    if (isSerializedRequest(e2.data)) {
      processRequest(e2.data);
    }
  };
};
var __viteBrowserExternal = {};
var __viteBrowserExternal$1 = /* @__PURE__ */ Object.freeze({
  __proto__: null,
  default: __viteBrowserExternal
});
`],{type:`text/javascript;charset=utf-8`});var X=Uint8Array,kt=Uint16Array,At=Int32Array,jt=new X([0,0,0,0,0,0,0,0,1,1,1,1,2,2,2,2,3,3,3,3,4,4,4,4,5,5,5,5,0,0,0,0]),Mt=new X([0,0,0,0,1,1,2,2,3,3,4,4,5,5,6,6,7,7,8,8,9,9,10,10,11,11,12,12,13,13,0,0]);new X([16,17,18,0,8,7,9,6,10,5,11,4,12,3,13,2,14,1,15]);var Nt=function(e,t){for(var n=new kt(31),r=0;r<31;++r)n[r]=t+=1<<e[r-1];for(var i=new At(n[30]),r=1;r<30;++r)for(var a=n[r];a<n[r+1];++a)i[a]=a-n[r]<<5|r;return{b:n,r:i}},Pt=Nt(jt,2),Ft=Pt.b,It=Pt.r;Ft[28]=258,It[258]=28;var Lt=Nt(Mt,0);Lt.b,Lt.r;for(var Rt=new kt(32768),Z=0;Z<32768;++Z){var Q=(Z&43690)>>1|(Z&21845)<<1;Q=(Q&52428)>>2|(Q&13107)<<2,Q=(Q&61680)>>4|(Q&3855)<<4,Rt[Z]=((Q&65280)>>8|(Q&255)<<8)>>1}for(var $=new X(288),Z=0;Z<144;++Z)$[Z]=8;for(var Z=144;Z<256;++Z)$[Z]=9;for(var Z=256;Z<280;++Z)$[Z]=7;for(var Z=280;Z<288;++Z)$[Z]=8;for(var zt=new X(32),Z=0;Z<32;++Z)zt[Z]=5;var Bt=/* @__PURE__ */ new X(0),Vt=typeof TextDecoder<`u`&&/* @__PURE__ */ new TextDecoder;try{Vt.decode(Bt,{stream:!0})}catch{}let Ht=``+new URL(`/assets/harper_wasm_bg-BEyNQVrT.wasm`,``+self.location.href).href,Ut=/* @__PURE__ */ xt.create(Ht,`full`),Wt;function Gt(){return Wt??=(async()=>{let e=new Ot({binary:Ut});return await e.setup(),`LongSentences`in await e.getDefaultLintConfig()&&await e.setLintConfig({LongSentences:!1}),e})()}function Kt(e){return e.replace(/\S+/g,e=>{let t=e.replace(/^[('"[{]+|[)'"\]},;:!?]+$/g,``);return/[/\\@_$=]/.test(t)||/^-{1,2}[\w]/.test(t)||/[a-z][A-Z]|[A-Za-z]\d|\d[A-Za-z]|[A-Za-z0-9]\.[A-Za-z]|^\.[\w]/.test(t)?e.replace(/[^`]/g,` `):e})}let qt=/* @__PURE__ */ new Set([`Spelling`,`Typo`,`Agreement`,`Grammar`,`Repetition`,`Punctuation`,`Capitalization`,`Miscellaneous`,`WordOrder`,`BoundaryError`]);async function Jt(e){if(!e.trim()||e.length>12e3)return[];let t=await(await Gt()).lint(Kt(e),{language:`markdown`}),n=[];try{for(let r of t){let t=r.lint_kind();if(!qt.has(t))continue;let i=r.span(),{start:a,end:o}=i;i.free();let s=e.slice(a,o);if(a<0||o<=a||o>e.length||s!==r.get_problem_text())continue;let c=r.suggestions();try{let e=[...new Set(c.map(e=>{let t=e.get_replacement_text();return e.kind()===te.InsertAfter?s+t:t}))].filter(e=>e!==s);e.length&&n.push({start:a,end:o,kind:t===`Spelling`||t===`Typo`?`spelling`:`grammar`,message:r.message(),replacements:e,original:s})}finally{for(let e of c)e.free()}}}finally{for(let e of t)e.free()}return n.sort((e,t)=>e.start-t.start)}typeof self<`u`&&(self.onmessage=async({data:e})=>{try{self.postMessage({id:e.id,issues:await Jt(e.text)})}catch{self.postMessage({id:e.id,error:`Writing suggestions are unavailable.`})}})})();