"use strict";
window.api={
  async request(path,options={}){const response=await fetch(path,options);let body=null;const type=response.headers.get("content-type")||"";if(type.includes("json"))body=await response.json();if(!response.ok){const error=new Error(body?.error?.message||`HTTP ${response.status}`);error.details=body?.error?.details;throw error}return body?.data},
  get(path){return this.request(path)},post(path,data){return this.request(path,{method:"POST",headers:data?{"Content-Type":"application/json"}:undefined,body:data?JSON.stringify(data):undefined})},put(path,data){return this.request(path,{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify(data)})},delete(path,data){return this.request(path,{method:"DELETE",headers:data?{"Content-Type":"application/json"}:undefined,body:data?JSON.stringify(data):undefined})},
  upload(path,file){const form=new FormData();form.append("file",file);return this.request(path,{method:"POST",body:form})}
};
window.showError=(error)=>{const box=document.querySelector("#notice");box.hidden=false;box.textContent=[error.message,...(error.details||[]).map(x=>`第 ${x.row||"-"} 行 ${x.field}: ${x.message}`)].join("；")};
